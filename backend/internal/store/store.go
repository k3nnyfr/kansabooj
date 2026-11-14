// Package store persists API state and history in SQLite.
package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/diff"
)

// ErrConcurrentUpdate means another check recorded a version of the same API in the meantime.
var ErrConcurrentUpdate = errors.New("version enregistrée entre-temps par un autre check")

//go:embed migrations/sqlite/*.sql migrations/postgres/*.sql
var migrations embed.FS

type Store struct {
	db         *rdb
	path       string
	lockPrefix string // distinguishes several Kansabooj instances sharing one PostgreSQL database
	applied    []string
}

// Options configures the database connection.
type Options struct {
	DSN             string // postgres:// URL, "postgres" (PG* environment variables) or SQLite file path
	Schema          string // PostgreSQL: dedicated schema (search_path); created if Migrate
	ApplicationName string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	// Migrate applies pending migrations at startup. When false (DDL handled by a DBA
	// or a dedicated job running `kansabooj migrate`), Open fails if migrations are pending.
	Migrate bool
}

// Open opens the database designated by dsn with default options and applies migrations.
func Open(ctx context.Context, dsn string) (*Store, error) {
	return OpenWith(ctx, Options{DSN: dsn, Migrate: true})
}

// OpenWith opens the database and applies (or checks) migrations.
func OpenWith(ctx context.Context, o Options) (*Store, error) {
	dialect := DialectOf(o.DSN)
	var db *sql.DB
	var err error
	if dialect == Postgres {
		db, err = openPostgres(ctx, o)
	} else {
		db, err = sql.Open("sqlite", "file:"+o.DSN+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
		if err == nil {
			db.SetMaxOpenConns(1) // single writer; volume is tiny
		}
	}
	if err != nil {
		return nil, fmt.Errorf("connexion à la base (%s) : %w", dialect, err)
	}
	s := &Store{db: &rdb{DB: db, dialect: dialect}, path: o.DSN, lockPrefix: o.Schema}
	if dialect == Postgres && o.Schema != "" && o.Migrate {
		// Create the schema only when missing: a DBA may have created it beforehand and
		// granted it to an application role without CREATE privilege on the database.
		var exists bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, o.Schema).Scan(&exists)
		if err == nil && !exists {
			_, err = db.ExecContext(ctx, `CREATE SCHEMA "`+o.Schema+`"`) // name validated by config (^[a-z_][a-z0-9_]*$)
		}
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("schéma %s : %w", o.Schema, err)
		}
	}
	if err := s.migrate(ctx, !o.Migrate); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// minPostgresConns: leader lock + a per-API check lock + the check's own queries + an HTTP request.
const minPostgresConns = 4

func openPostgres(ctx context.Context, o Options) (*sql.DB, error) {
	dsn := o.DSN
	if dsn == "postgres" {
		dsn = "" // libpq environment variables: PGHOST, PGUSER, PGPASSWORD, PGDATABASE, PGSSLMODE…
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if o.Schema != "" {
		cfg.RuntimeParams["search_path"] = o.Schema
	}
	if o.ApplicationName != "" {
		cfg.RuntimeParams["application_name"] = o.ApplicationName
	}
	db := stdlib.OpenDB(*cfg)
	// Session advisory locks pin connections (leader lock + one per running check):
	// keep enough room for the queries themselves.
	conns := o.MaxOpenConns
	if conns == 0 {
		conns = 10
	}
	db.SetMaxOpenConns(max(conns, minPostgresConns))
	if o.MaxIdleConns > 0 {
		db.SetMaxIdleConns(o.MaxIdleConns)
	}
	if o.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(o.ConnMaxLifetime)
	}
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Dialect returns "postgres" or "sqlite".
func (s *Store) Dialect() string { return s.db.dialect }

// Ping checks the database is reachable (readiness probe).
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Applied returns the migrations applied by this Open (empty when up to date).
func (s *Store) Applied() []string { return s.applied }

func (s *Store) migrate(ctx context.Context, checkOnly bool) error {
	if s.db.dialect == Postgres && !checkOnly {
		// Several replicas may start at once: serialize migrations.
		conn, err := s.db.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		key := s.lockName("migrations")
		if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtext($1))`, key); err != nil {
			return fmt.Errorf("verrou de migration : %w", err)
		}
		defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtext($1))`, key)
	}

	dir := "migrations/" + s.db.dialect
	paths, err := fs.Glob(migrations, dir+"/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(paths)

	if checkOnly {
		done := map[string]bool{}
		rows, err := s.db.QueryContext(ctx, `SELECT name FROM schema_migrations`)
		if err != nil {
			return fmt.Errorf("schéma non initialisé : exécuter « kansabooj migrate » avec un compte disposant des droits DDL (%w)", err)
		}
		for rows.Next() {
			var n string
			rows.Scan(&n)
			done[n] = true
		}
		rows.Close()
		var pending []string
		for _, p := range paths {
			if !done[path.Base(p)] {
				pending = append(pending, path.Base(p))
			}
		}
		if len(pending) > 0 {
			return fmt.Errorf("migrations en attente (%s) : exécuter « kansabooj migrate » avec un compte disposant des droits DDL", strings.Join(pending, ", "))
		}
		return nil
	}

	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMP NOT NULL)`); err != nil {
		return err
	}
	// names used to include the directory (v0.1/v0.2, SQLite only)
	if _, err := s.db.ExecContext(ctx, `UPDATE schema_migrations SET name = replace(name, 'migrations/', '') WHERE name LIKE 'migrations/%'`); err != nil {
		return err
	}
	for _, p := range paths {
		name := path.Base(p)
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, _ := migrations.ReadFile(p)
		err := s.tx(ctx, func(tx *rtx) error {
			if _, err := tx.Tx.ExecContext(ctx, string(body)); err != nil { // no placeholders: multi-statement script
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`, name, now())
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s : %w", name, err)
		}
		s.applied = append(s.applied, name)
	}
	return nil
}

// SyncAPIs mirrors the configuration: upserts declared APIs and disables the
// others. History is never deleted.
func (s *Store) SyncAPIs(ctx context.Context, apis []config.API) error {
	return s.tx(ctx, func(tx *rtx) error {
		ids := make([]any, 0, len(apis))
		for _, a := range apis {
			ids = append(ids, a.ID)
			_, err := tx.ExecContext(ctx, `
INSERT INTO apis (id, name, owner, source_url, source_type, config_json, group_id, tags, network_profile, enabled, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, TRUE, ?)
ON CONFLICT (id) DO UPDATE SET
  name = excluded.name, owner = excluded.owner, source_type = excluded.source_type,
  group_id = excluded.group_id, tags = excluded.tags, network_profile = excluded.network_profile,
  config_json = excluded.config_json, enabled = TRUE, updated_at = excluded.updated_at,
  -- a new source URL invalidates the HTTP validators
  etag = CASE WHEN apis.source_url = excluded.source_url THEN apis.etag END,
  last_modified = CASE WHEN apis.source_url = excluded.source_url THEN apis.last_modified END,
  source_url = excluded.source_url`,
				a.ID, a.Name, a.Owner, a.Source.URL, a.Source.Type, a.PublicJSON(), nullIfEmpty(a.Group), tagsJSON(a.Tags), a.Network, now())
			if err != nil {
				return fmt.Errorf("sync %s : %w", a.ID, err)
			}
		}
		q := `UPDATE apis SET enabled = FALSE, updated_at = ? WHERE enabled = TRUE`
		args := []any{now()}
		if len(ids) > 0 {
			q += ` AND id NOT IN (?` + strings.Repeat(",?", len(ids)-1) + `)`
			args = append(args, ids...)
		}
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	})
}

// APIState is the mutable state of a monitored API.
type APIState struct {
	ID                string
	ResolvedURL       string // spec URL found by discovery (or the source URL)
	Status            string
	ConsecutiveErrors int
	ETag              string
	LastModified      string
	Current           *Snapshot
}

type Snapshot struct {
	ID               int64
	FetchedAt        time.Time
	RawSHA256        string
	NormalizedSHA256 string
	Format           string
	Version          string
	Title            string
	OperationCount   int
}

func (s *Store) State(ctx context.Context, apiID string) (*APIState, error) {
	st := &APIState{ID: apiID}
	var etag, lm, resolved sql.NullString
	var snapID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT status, consecutive_errors, etag, last_modified, resolved_spec_url, current_snapshot_id FROM apis WHERE id = ?`, apiID).
		Scan(&st.Status, &st.ConsecutiveErrors, &etag, &lm, &resolved, &snapID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("API %q inconnue en base (configuration non synchronisée ?)", apiID)
	}
	if err != nil {
		return nil, err
	}
	st.ETag, st.LastModified, st.ResolvedURL = etag.String, lm.String, resolved.String
	if snapID.Valid {
		st.Current = &Snapshot{ID: snapID.Int64}
		var ver, title sql.NullString
		err = s.db.QueryRowContext(ctx, `SELECT fetched_at, raw_sha256, normalized_sha256, spec_format, info_version, title, operation_count FROM snapshots WHERE id = ?`, snapID.Int64).
			Scan(&st.Current.FetchedAt, &st.Current.RawSHA256, &st.Current.NormalizedSHA256, &st.Current.Format, &ver, &title, &st.Current.OperationCount)
		if err != nil {
			return nil, err
		}
		st.Current.Version, st.Current.Title = ver.String, title.String
	}
	return st, nil
}

// SetResolvedURL records the spec URL actually fetched for an API. When it changes,
// the HTTP validators (ETag…) of the previous URL are dropped.
func (s *Store) SetResolvedURL(ctx context.Context, apiID, u string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE apis SET
  etag = CASE WHEN resolved_spec_url IS NOT DISTINCT FROM ? THEN etag END,
  last_modified = CASE WHEN resolved_spec_url IS NOT DISTINCT FROM ? THEN last_modified END,
  resolved_spec_url = ? WHERE id = ?`, u, u, u, apiID)
	return err
}

// Blob returns the decompressed content of a stored blob.
func (s *Store) Blob(ctx context.Context, sha string) ([]byte, error) {
	var enc string
	var content []byte
	if err := s.db.QueryRowContext(ctx, `SELECT encoding, content FROM blobs WHERE sha256 = ?`, sha).Scan(&enc, &content); err != nil {
		return nil, fmt.Errorf("blob %s : %w", sha, err)
	}
	if enc != "gzip" {
		return nil, fmt.Errorf("blob %s : encodage %q non supporté", sha, enc)
	}
	zr, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}

// CheckRecord is one execution of a check.
type CheckRecord struct {
	APIID      string
	StartedAt  time.Time
	Duration   time.Duration
	Trigger    string // schedule | manual | startup
	Actor      string // who asked for a manual check (SSO user, token:<name>, anonymous, cli); empty otherwise
	Status     string // unchanged | changed | error
	ErrorKind  string
	HTTPStatus int
	Error      string
	ETag       string
	LastMod    string
}

// RecordUnchanged logs a successful check without contract change.
func (s *Store) RecordUnchanged(ctx context.Context, c CheckRecord) error {
	return s.tx(ctx, func(tx *rtx) error {
		if err := insertCheck(ctx, tx, c, nil); err != nil {
			return err
		}
		return markOK(ctx, tx, c)
	})
}

// RecordError logs a failed check and returns the new consecutive error count.
// Errors never create snapshots or changes.
func (s *Store) RecordError(ctx context.Context, c CheckRecord, staleThreshold int) (int, error) {
	var count int
	err := s.tx(ctx, func(tx *rtx) error {
		if err := insertCheck(ctx, tx, c, nil); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `UPDATE apis SET consecutive_errors = consecutive_errors + 1 WHERE id = ? RETURNING consecutive_errors`, c.APIID).Scan(&count); err != nil {
			return err
		}
		status := "error"
		switch {
		case count >= staleThreshold:
			status = "stale"
		case c.ErrorKind == "auth":
			status = "auth_error"
		}
		_, err := tx.ExecContext(ctx, `UPDATE apis SET status = ?, last_error = ?, last_checked_at = ? WHERE id = ?`, status, c.Error, c.StartedAt, c.APIID)
		return err
	})
	return count, err
}

// NewVersion is a detected contract change to persist atomically.
type NewVersion struct {
	Check           CheckRecord
	Raw             []byte
	RawMedia        string
	Normalized      []byte
	NormalizedMedia string // default application/json
	Snapshot        Snapshot
	PrevSnapID      *int64
	Summary         *diff.Summary
	Semantic        bool
}

// SaveNewVersion stores blobs, snapshot, change and items, and moves the API to it.
// It returns the change id.
func (s *Store) SaveNewVersion(ctx context.Context, v NewVersion) (int64, error) {
	var changeID int64
	err := s.tx(ctx, func(tx *rtx) error {
		rawSHA, err := putBlob(ctx, tx, v.Raw, v.RawMedia)
		if err != nil {
			return err
		}
		media := v.NormalizedMedia
		if media == "" {
			media = "application/json"
		}
		normSHA, err := putBlob(ctx, tx, v.Normalized, media)
		if err != nil {
			return err
		}
		var snapID int64
		err = tx.QueryRowContext(ctx, `
INSERT INTO snapshots (api_id, fetched_at, raw_sha256, normalized_sha256, spec_format, info_version, title, operation_count)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (api_id, normalized_sha256) DO UPDATE SET fetched_at = excluded.fetched_at
RETURNING id`,
			v.Check.APIID, v.Check.StartedAt, rawSHA, normSHA, v.Snapshot.Format, v.Snapshot.Version, v.Snapshot.Title, v.Snapshot.OperationCount).Scan(&snapID)
		if err != nil {
			return fmt.Errorf("snapshot : %w", err)
		}

		sm := v.Summary
		err = tx.QueryRowContext(ctx, `
INSERT INTO changes (api_id, from_snapshot_id, to_snapshot_id, detected_at, version_from, version_to,
                     error_count, warn_count, info_count, in_scope_count, semantic)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			v.Check.APIID, v.PrevSnapID, snapID, v.Check.StartedAt, sm.VersionFrom, sm.VersionTo,
			sm.Errors, sm.Warns, sm.Infos, sm.InScope, v.Semantic).Scan(&changeID)
		if err != nil {
			return fmt.Errorf("change : %w", err)
		}
		for _, it := range sm.Items {
			if _, err := tx.ExecContext(ctx, `INSERT INTO change_items (change_id, rule_id, level, method, path, in_scope, text, text_fr) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				changeID, it.RuleID, it.Level, it.Method, it.Path, it.InScope, it.Text, nullIfEmpty(it.TextFR)); err != nil {
				return err
			}
		}
		if err := insertCheck(ctx, tx, v.Check, &snapID); err != nil {
			return err
		}
		// Optimistic concurrency: another replica may have recorded a version meanwhile.
		res, err := tx.ExecContext(ctx, `UPDATE apis SET current_snapshot_id = ? WHERE id = ? AND current_snapshot_id IS NOT DISTINCT FROM ?`, snapID, v.Check.APIID, v.PrevSnapID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrConcurrentUpdate
		}
		return markOK(ctx, tx, v.Check)
	})
	return changeID, err
}

// Renormalize replaces the canonical form of a snapshot by an equivalent one produced by
// a newer normalization (normalize.Upgrade), so that the API's hash matches the new form
// without recording a change. It returns false, without touching anything, when another
// snapshot of the API already has that form.
func (s *Store) Renormalize(ctx context.Context, snapID int64, canonical []byte) (bool, error) {
	done := false
	err := s.tx(ctx, func(tx *rtx) error {
		sum := sha256.Sum256(canonical)
		sha := hex.EncodeToString(sum[:])
		var taken int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM snapshots o JOIN snapshots s ON s.api_id = o.api_id
WHERE s.id = ? AND o.id <> s.id AND o.normalized_sha256 = ?`, snapID, sha).Scan(&taken); err != nil {
			return err
		}
		if taken > 0 {
			return nil
		}
		if _, err := putBlob(ctx, tx, canonical, "application/json"); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE snapshots SET normalized_sha256 = ? WHERE id = ?`, sha, snapID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		done = n == 1
		return nil
	})
	return done, err
}

// ChangeSummary is a timeline entry.
type ChangeSummary struct {
	ID                            int64
	DetectedAt                    time.Time
	VersionFrom, VersionTo        string
	Errors, Warns, Infos, InScope int
}

// Timeline returns the latest changes of an API, most recent first.
func (s *Store) Timeline(ctx context.Context, apiID string, limit int) ([]ChangeSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, detected_at, COALESCE(version_from,''), COALESCE(version_to,''), error_count, warn_count, info_count, in_scope_count
FROM changes WHERE api_id = ? ORDER BY detected_at DESC, id DESC LIMIT ?`, apiID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChangeSummary
	for rows.Next() {
		var c ChangeSummary
		if err := rows.Scan(&c.ID, &c.DetectedAt, &c.VersionFrom, &c.VersionTo, &c.Errors, &c.Warns, &c.Infos, &c.InScope); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func insertCheck(ctx context.Context, tx *rtx, c CheckRecord, snapID *int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO checks (api_id, started_at, duration_ms, trigger, status, error_kind, http_status, error, snapshot_id, actor)
VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, 0), NULLIF(?, ''), ?, NULLIF(?, ''))`,
		c.APIID, c.StartedAt, c.Duration.Milliseconds(), c.Trigger, c.Status, c.ErrorKind, c.HTTPStatus, c.Error, snapID, c.Actor)
	return err
}

func markOK(ctx context.Context, tx *rtx, c CheckRecord) error {
	_, err := tx.ExecContext(ctx, `UPDATE apis SET status = 'ok', last_error = NULL, consecutive_errors = 0, last_checked_at = ?,
  etag = COALESCE(NULLIF(?, ''), etag), last_modified = COALESCE(NULLIF(?, ''), last_modified) WHERE id = ?`,
		c.StartedAt, c.ETag, c.LastMod, c.APIID)
	return err
}

func putBlob(ctx context.Context, tx *rtx, content []byte, media string) (string, error) {
	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Write(content)
	zw.Close()
	_, err := tx.ExecContext(ctx, `INSERT INTO blobs (sha256, encoding, media_type, size, content) VALUES (?, 'gzip', ?, ?, ?) ON CONFLICT DO NOTHING`,
		sha, media, len(content), buf.Bytes())
	return sha, err
}

func (s *Store) tx(ctx context.Context, fn func(*rtx) error) error {
	sqlTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	tx := &rtx{Tx: sqlTx, dialect: s.db.dialect}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func now() time.Time { return time.Now().UTC() }

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func tagsJSON(tags []string) string {
	if len(tags) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(tags)
	return string(b)
}
