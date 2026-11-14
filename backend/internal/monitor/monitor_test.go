package monitor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/diff"
	"git.k3nny.fr/kansabooj/internal/normalize"
	"git.k3nny.fr/kansabooj/internal/store"
	"git.k3nny.fr/kansabooj/internal/store/storetest"
)

const v1 = `openapi: 3.0.3
info: { title: Orders, version: "1.0" }
servers: [{ url: "https://a.example" }]
paths:
  /orders:
    get:
      parameters: [{ name: status, in: query, schema: { type: string } }]
      responses: { "200": { description: ok } }
`

const v1OtherServer = `openapi: 3.0.3
info: { title: Orders, version: "1.0" }
servers: [{ url: "https://b.example" }]
paths:
  /orders:
    get:
      parameters: [{ name: status, in: query, schema: { type: string } }]
      responses: { "200": { description: ok } }
`

const v2 = `openapi: 3.0.3
info: { title: Orders, version: "1.1" }
paths:
  /orders:
    get:
      parameters:
        - { name: status, in: query, schema: { type: string } }
        - { name: tenant, in: query, required: true, schema: { type: string } }
      responses: { "200": { description: ok } }
`

func TestCheckLifecycle(t *testing.T) {
	var body atomic.Value
	var status atomic.Int32
	body.Store(v1)
	status.Store(200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := int(status.Load()); s != 200 {
			w.WriteHeader(s)
			return
		}
		b := body.Load().(string)
		etag := fmt.Sprintf(`"%x"`, sha256.Sum256([]byte(b)))
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Type", "text/x-yaml")
		w.Write([]byte(b))
	}))
	defer srv.Close()

	cfg, err := config.Parse([]byte(`
defaults: { ignore: [$.servers], error_threshold: 2 }
apis:
  - id: orders
    name: Orders
    source: { url: "` + srv.URL + `/openapi.yaml", type: openapi }
`))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st := storetest.Open(t)
	defer st.Close()
	if err := st.SyncAPIs(ctx, cfg.APIs); err != nil {
		t.Fatal(err)
	}
	m := New(cfg, st)
	check := func(want string) *Outcome {
		t.Helper()
		o, err := m.Check(ctx, "orders", "manual", false)
		if err != nil {
			t.Fatal(err)
		}
		if o.Status != want {
			t.Fatalf("status = %s (err=%v), want %s", o.Status, o.Err, want)
		}
		return o
	}

	if o := check(StatusChanged); o.Summary.Items[0].RuleID != "first-capture" {
		t.Errorf("first check should be a first capture: %+v", o.Summary.Items)
	}
	if o := check(StatusUnchanged); !o.NotModified {
		t.Error("second check should use the ETag (304)")
	}
	body.Store(v1OtherServer) // only an ignored field changes
	if o := check(StatusUnchanged); o.NotModified {
		t.Error("new body should have been downloaded")
	}

	status.Store(503)
	check(StatusError)
	if o := check(StatusError); o.ConsecutiveErrors != 2 {
		t.Errorf("consecutive errors = %d", o.ConsecutiveErrors)
	}
	if s, _ := st.State(ctx, "orders"); s.Status != "stale" {
		t.Errorf("status after threshold = %s", s.Status)
	}

	status.Store(200)
	body.Store(v2)
	o := check(StatusChanged)
	if o.Summary.Errors == 0 || o.Summary.VersionFrom != "1.0" || o.Summary.VersionTo != "1.1" {
		t.Errorf("unexpected summary %+v", o.Summary)
	}
	if s, _ := st.State(ctx, "orders"); s.Status != "ok" || s.ConsecutiveErrors != 0 || s.Current.Version != "1.1" {
		t.Errorf("state not reset after recovery: %+v", s)
	}
	tl, err := st.Timeline(ctx, "orders", 10)
	if err != nil || len(tl) != 2 {
		t.Fatalf("timeline = %+v, %v", tl, err)
	}

	// dry run on the same content: unchanged, nothing written
	if o, _ := m.Check(ctx, "orders", "manual", true); o.Status != StatusUnchanged {
		t.Errorf("dry run status = %s", o.Status)
	}
}

// A version stored before a normalization change (here: enum values sorted) must not
// produce a "phantom" evolution: it is brought to the new form in place.
func TestOlderNormalizationIsNotAChange(t *testing.T) {
	spec := `openapi: 3.0.3
info: { title: Tasks, version: "1.0" }
paths:
  /tasks:
    get:
      parameters: [{ name: status, in: query, schema: { type: string, enum: [RETRY, FAILURE, PENDING] } }]
      responses: { "200": { description: ok } }
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/x-yaml")
		w.Write([]byte(spec))
	}))
	defer srv.Close()
	cfg, err := config.Parse([]byte(`
apis:
  - id: tasks
    name: Tasks
    source: { url: "` + srv.URL + `/openapi.yaml", type: openapi }
`))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st := storetest.Open(t)
	defer st.Close()
	if err := st.SyncAPIs(ctx, cfg.APIs); err != nil {
		t.Fatal(err)
	}

	// what an older Kansabooj stored: canonical JSON with the enum in the producer's order
	cur, err := normalize.Normalize([]byte(spec), nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy := bytes.Replace(cur.Canonical, []byte(`"FAILURE",`), nil, 1)
	legacy = bytes.Replace(legacy, []byte(`"PENDING",`), []byte(`"PENDING",`+"\n"+`                "FAILURE",`), 1)
	if _, sha, _ := normalize.Upgrade(legacy); bytes.Equal(legacy, cur.Canonical) || sha != cur.SHA256 {
		t.Fatalf("test setup: legacy form not built\n%s", legacy)
	}
	if _, err := st.SaveNewVersion(ctx, store.NewVersion{
		Check:      store.CheckRecord{APIID: "tasks", StartedAt: time.Now().UTC(), Trigger: "schedule", Status: "changed"},
		Raw:        []byte(spec),
		RawMedia:   "application/yaml",
		Normalized: legacy,
		Snapshot:   store.Snapshot{Format: "openapi-3.0", Version: "1.0", Title: "Tasks", OperationCount: 1},
		Summary:    &diff.Summary{Items: []diff.Item{}},
		Semantic:   true,
	}); err != nil {
		t.Fatal(err)
	}

	m := New(cfg, st)
	for i := range 2 { // first: brought up to date in place; second: plain hash match
		o, err := m.Check(ctx, "tasks", "manual", false)
		if err != nil || o.Status != StatusUnchanged {
			t.Fatalf("check %d: status = %s, err = %v / %v", i, o.Status, err, o.Err)
		}
	}
	if tl, _ := st.Timeline(ctx, "tasks", 10); len(tl) != 1 {
		t.Errorf("phantom change recorded: %+v", tl)
	}
	if s, _ := st.State(ctx, "tasks"); s.Current.NormalizedSHA256 != cur.SHA256 {
		t.Errorf("current version not brought to the new form: %s", s.Current.NormalizedSHA256)
	}
}
