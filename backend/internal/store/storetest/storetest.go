// Package storetest opens a throwaway store for tests: SQLite by default, or a fresh
// PostgreSQL database when KANSABOOJ_TEST_POSTGRES holds a DSN (e.g. in CI):
//
//	KANSABOOJ_TEST_POSTGRES=postgres://user@localhost:5432/postgres?sslmode=disable go test ./...
package storetest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"git.k3nny.fr/kansabooj/internal/store"
)

func Open(t testing.TB) *store.Store {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("KANSABOOJ_TEST_POSTGRES")
	if dsn == "" {
		st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		return st
	}

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 6)
	rand.Read(b)
	name := "kansabooj_test_" + hex.EncodeToString(b)
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+name)
		admin.Close()
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	st, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() }) // runs before the DROP above
	return st
}
