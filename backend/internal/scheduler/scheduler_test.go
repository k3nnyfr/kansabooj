package scheduler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/monitor"
	"git.k3nny.fr/kansabooj/internal/store/storetest"
)

const spec = `{"openapi":"3.0.3","info":{"title":"T","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok"}}}}}}`

func TestScheduleManualAndReload(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(spec))
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "apis.yml")
	write := func(extra string) {
		os.WriteFile(path, []byte(`
defaults: { interval: 1h, jitter: 1ms }
apis:
  - { id: a, name: A, source: { url: "`+srv.URL+`/a.json" } }
`+extra), 0o600)
	}
	write("")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := storetest.Open(t)
	defer st.Close()
	st.SyncAPIs(ctx, cfg.APIs)

	s, err := New(path, cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s.tick = 20 * time.Millisecond
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	waitFor(t, func() bool { r, _ := st.GetAPI(ctx, "a"); return r.Status == "ok" && len(s.Status().Running) == 0 })
	if n := hits.Load(); n != 1 {
		t.Errorf("scheduled check hits = %d", n)
	}
	if next := s.Status().NextChecks["a"]; time.Until(next) < 50*time.Minute {
		t.Errorf("next check should be ~1h later, got %v", next)
	}

	o, err := s.TriggerCheck(ctx, "a")
	if err != nil || o.Status != monitor.StatusUnchanged {
		t.Fatalf("manual check: %+v %v", o, err)
	}
	if _, err := s.TriggerCheck(ctx, "nope"); err != ErrUnknownAPI {
		t.Errorf("unknown api: %v", err)
	}

	write(`  - { id: b, name: B, source: { url: "` + srv.URL + `/b.json" } }`)
	if err := s.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { r, _ := st.GetAPI(ctx, "b"); return r != nil && r.Status == "ok" })

	os.WriteFile(path, []byte("apis: [ {id: BAD} ]"), 0o600)
	if err := s.Reload(ctx); err == nil {
		t.Error("invalid config must be rejected")
	}
	if len(s.Config().APIs) != 2 {
		t.Error("previous config must be kept after a failed reload")
	}
	cancel()
	<-done
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSingleLeaderAcrossInstances(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(spec)) }))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "apis.yml")
	os.WriteFile(path, []byte(`
defaults: { interval: 1h, jitter: 1ms }
apis: [ { id: a, name: A, source: { url: "`+srv.URL+`/a.json" } } ]
`), 0o600)
	cfg, _ := config.Load(path)
	st := storetest.Open(t)
	st.SyncAPIs(context.Background(), cfg.APIs)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	start := func() (*Scheduler, context.CancelFunc, chan struct{}) {
		s, err := New(path, cfg, st, log)
		if err != nil {
			t.Fatal(err)
		}
		s.tick, s.retry = 20*time.Millisecond, 20*time.Millisecond
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { s.Run(ctx); close(done) }()
		return s, cancel, done
	}
	s1, stop1, done1 := start()
	waitFor(t, func() bool { return s1.Status().Leader })
	s2, stop2, done2 := start()
	defer func() { stop2(); <-done2 }()
	time.Sleep(100 * time.Millisecond)
	if s2.Status().Leader {
		t.Fatal("two leaders at once")
	}
	stop1()
	<-done1
	waitFor(t, func() bool { return s2.Status().Leader })
}

func TestWatchConfigReloadsOnSecretChange(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "key")
	os.WriteFile(secret, []byte("v1"), 0o600)
	path := filepath.Join(dir, "apis.yml")
	os.WriteFile(path, []byte(`
apis:
  - id: a
    name: A
    source: { url: "https://a.example/spec.json" }
    auth: { type: bearer, token: "${file:`+secret+`}" }
`), 0o600)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	st := storetest.Open(t)
	s, _ := New(path, cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.WatchConfig(ctx, 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond) // let the watcher take its initial fingerprint
	os.WriteFile(secret, []byte("v2"), 0o600)
	waitFor(t, func() bool { a, _ := s.Config().API("a"); return a.Auth.Token == "v2" })
}
