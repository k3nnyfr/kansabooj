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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/diff"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ db *sql.DB }

// Open opens (or creates) the database and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // single writer; volume is tiny
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMP NOT NULL)`); err != nil {
		return err
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, _ := migrations.ReadFile(name)
		err := s.tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`, name, now())
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s : %w", name, err)
		}
	}
	return nil
}

// SyncAPIs mirrors the configuration: upserts declared APIs and disables the
// others. History is never deleted.
func (s *Store) SyncAPIs(ctx context.Context, apis []config.API) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		ids := make([]any, 0, len(apis))
		for _, a := range apis {
			ids = append(ids, a.ID)
			_, err := tx.ExecContext(ctx, `
INSERT INTO apis (id, name, owner, source_url, source_type, config_json, enabled, updated_at)
VALUES (?, ?, ?, ?, ?, ?, 1, ?)
ON CONFLICT (id) DO UPDATE SET
  name = excluded.name, owner = excluded.owner, source_type = excluded.source_type,
  config_json = excluded.config_json, enabled = 1, updated_at = excluded.updated_at,
  -- a new source URL invalidates the HTTP validators
  etag = CASE WHEN apis.source_url = excluded.source_url THEN apis.etag END,
  last_modified = CASE WHEN apis.source_url = excluded.source_url THEN apis.last_modified END,
  source_url = excluded.source_url`,
				a.ID, a.Name, a.Owner, a.Source.URL, a.Source.Type, a.PublicJSON(), now())
			if err != nil {
				return fmt.Errorf("sync %s : %w", a.ID, err)
			}
		}
		q := `UPDATE apis SET enabled = 0, updated_at = ? WHERE enabled = 1`
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
	var etag, lm sql.NullString
	var snapID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT status, consecutive_errors, etag, last_modified, current_snapshot_id FROM apis WHERE id = ?`, apiID).
		Scan(&st.Status, &st.ConsecutiveErrors, &etag, &lm, &snapID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("API %q inconnue en base (configuration non synchronisée ?)", apiID)
	}
	if err != nil {
		return nil, err
	}
	st.ETag, st.LastModified = etag.String, lm.String
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
	Status     string // unchanged | changed | error
	ErrorKind  string
	HTTPStatus int
	Error      string
	ETag       string
	LastMod    string
}

// RecordUnchanged logs a successful check without contract change.
func (s *Store) RecordUnchanged(ctx context.Context, c CheckRecord) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
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
	err := s.tx(ctx, func(tx *sql.Tx) error {
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
	Check      CheckRecord
	Raw        []byte
	RawMedia   string
	Normalized []byte
	Snapshot   Snapshot
	PrevSnapID *int64
	Summary    *diff.Summary
	Semantic   bool
}

// SaveNewVersion stores blobs, snapshot, change and items, and moves the API to it.
// It returns the change id.
func (s *Store) SaveNewVersion(ctx context.Context, v NewVersion) (int64, error) {
	var changeID int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		rawSHA, err := putBlob(ctx, tx, v.Raw, v.RawMedia)
		if err != nil {
			return err
		}
		normSHA, err := putBlob(ctx, tx, v.Normalized, "application/json")
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
			if _, err := tx.ExecContext(ctx, `INSERT INTO change_items (change_id, rule_id, level, method, path, in_scope, text) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				changeID, it.RuleID, it.Level, it.Method, it.Path, it.InScope, it.Text); err != nil {
				return err
			}
		}
		if err := insertCheck(ctx, tx, v.Check, &snapID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE apis SET current_snapshot_id = ? WHERE id = ?`, snapID, v.Check.APIID); err != nil {
			return err
		}
		return markOK(ctx, tx, v.Check)
	})
	return changeID, err
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

func insertCheck(ctx context.Context, tx *sql.Tx, c CheckRecord, snapID *int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO checks (api_id, started_at, duration_ms, trigger, status, error_kind, http_status, error, snapshot_id)
VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, 0), NULLIF(?, ''), ?)`,
		c.APIID, c.StartedAt, c.Duration.Milliseconds(), c.Trigger, c.Status, c.ErrorKind, c.HTTPStatus, c.Error, snapID)
	return err
}

func markOK(ctx context.Context, tx *sql.Tx, c CheckRecord) error {
	_, err := tx.ExecContext(ctx, `UPDATE apis SET status = 'ok', last_error = NULL, consecutive_errors = 0, last_checked_at = ?,
  etag = COALESCE(NULLIF(?, ''), etag), last_modified = COALESCE(NULLIF(?, ''), last_modified) WHERE id = ?`,
		c.StartedAt, c.ETag, c.LastMod, c.APIID)
	return err
}

func putBlob(ctx context.Context, tx *sql.Tx, content []byte, media string) (string, error) {
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

func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func now() time.Time { return time.Now().UTC() }
