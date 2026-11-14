package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"sync"
)

// Two engines are supported with the same SQL (written with "?" placeholders):
//   - PostgreSQL: the deployment target (Kubernetes / OpenShift), multi-replica capable;
//   - SQLite: local development, CLI, tests.
// Queries must stay portable: TRUE/FALSE for booleans, lower() instead of COLLATE NOCASE,
// RETURNING instead of LastInsertId, ON CONFLICT for upserts.

const (
	Postgres = "postgres"
	SQLite   = "sqlite"
)

// DialectOf infers the engine from a DSN: postgres:// or postgresql:// URLs (or the
// keyword "postgres", meaning "use the PG* environment variables") select PostgreSQL;
// anything else is a SQLite file path.
func DialectOf(dsn string) string {
	if dsn == "postgres" || strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return Postgres
	}
	return SQLite
}

// rebind turns ? placeholders into $1, $2… for PostgreSQL. Queries never contain a
// literal question mark.
func rebind(dialect, q string) string {
	if dialect != Postgres || !strings.Contains(q, "?") {
		return q
	}
	var b strings.Builder
	n := 0
	for _, r := range q {
		if r == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// rdb and rtx rebind every query for the active dialect.
type rdb struct {
	*sql.DB
	dialect string
}

func (d *rdb) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return d.DB.ExecContext(ctx, rebind(d.dialect, q), args...)
}
func (d *rdb) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return d.DB.QueryContext(ctx, rebind(d.dialect, q), args...)
}
func (d *rdb) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return d.DB.QueryRowContext(ctx, rebind(d.dialect, q), args...)
}

type rtx struct {
	*sql.Tx
	dialect string
}

func (t *rtx) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return t.Tx.ExecContext(ctx, rebind(t.dialect, q), args...)
}
func (t *rtx) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return t.Tx.QueryContext(ctx, rebind(t.dialect, q), args...)
}
func (t *rtx) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return t.Tx.QueryRowContext(ctx, rebind(t.dialect, q), args...)
}

// Lock is a named lock held until Release. With PostgreSQL it is a session advisory
// lock, shared by every replica; with SQLite (single instance) it is in-process.
type Lock struct {
	release func()
	conn    *sql.Conn
}

// Release frees the lock. Safe to call more than once.
func (l *Lock) Release() {
	if l != nil && l.release != nil {
		l.release()
		l.release = nil
	}
}

// Alive reports whether the lock is still held (the session holding it is up).
func (l *Lock) Alive(ctx context.Context) error {
	if l.conn == nil {
		return nil
	}
	return l.conn.PingContext(ctx)
}

var localLocks sync.Map // name → struct{}, for SQLite

func (s *Store) lockName(name string) string {
	if s.lockPrefix == "" {
		return "kansabooj:" + name
	}
	return "kansabooj:" + s.lockPrefix + ":" + name
}

// TryLock acquires the named lock without waiting; ok is false when someone else holds it.
// Names are scoped to the schema, so instances sharing a database do not interfere.
func (s *Store) TryLock(ctx context.Context, name string) (lock *Lock, ok bool, err error) {
	name = s.lockName(name)
	if s.db.dialect != Postgres {
		key := s.path + "\x00" + name
		if _, held := localLocks.LoadOrStore(key, struct{}{}); held {
			return nil, false, nil
		}
		return &Lock{release: func() { localLocks.Delete(key) }}, true, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, name).Scan(&ok); err != nil || !ok {
		conn.Close()
		return nil, false, err
	}
	return &Lock{conn: conn, release: func() {
		conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtext($1))`, name)
		conn.Close()
	}}, true, nil
}
