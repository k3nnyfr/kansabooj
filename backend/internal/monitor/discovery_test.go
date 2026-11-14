package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/diff"
	"git.k3nny.fr/kansabooj/internal/store/storetest"
)

type fakeSite struct {
	mu    sync.Mutex
	files map[string]string
	hits  map[string]int
}

func (f *fakeSite) set(path, body string) { f.mu.Lock(); f.files[path] = body; f.mu.Unlock() }
func (f *fakeSite) del(path string)       { f.mu.Lock(); delete(f.files, path); f.mu.Unlock() }
func (f *fakeSite) count(path string) int { f.mu.Lock(); defer f.mu.Unlock(); return f.hits[path] }

func (f *fakeSite) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits[r.URL.Path]++
	body, ok := f.files[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	etag := `"` + r.URL.Path + "-" + string(rune('a'+len(body)%26)) + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	if strings.HasPrefix(strings.TrimSpace(body), "<") {
		w.Header().Set("Content-Type", "text/html")
	}
	w.Write([]byte(body))
}

func newSite(t *testing.T, files map[string]string) (*fakeSite, string) {
	f := &fakeSite{files: files, hits: map[string]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func monitorFor(t *testing.T, yaml string) (*Monitor, func(want string) *Outcome) {
	t.Helper()
	cfg, err := config.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	st := storetest.Open(t)
	st.SyncAPIs(context.Background(), cfg.APIs)
	m := New(cfg, st)
	return m, func(want string) *Outcome {
		t.Helper()
		o, err := m.Check(context.Background(), cfg.APIs[0].ID, "manual", false)
		if err != nil {
			t.Fatal(err)
		}
		if o.Status != want {
			t.Fatalf("status = %s (%v), want %s", o.Status, o.Err, want)
		}
		return o
	}
}

const specA = `{"openapi":"3.0.3","info":{"title":"T","version":"1"},"paths":{"/a":{"get":{"responses":{"200":{"description":"ok"}}}}}}`

func TestSwaggerUIDiscoveryLifecycle(t *testing.T) {
	site, base := newSite(t, map[string]string{
		"/docs/":                       `<html><script src="swagger-initializer.js"></script></html>`,
		"/docs/swagger-initializer.js": `SwaggerUIBundle({ url: "/v1/openapi.json" })`,
		"/v1/openapi.json":             specA,
	})
	_, check := monitorFor(t, `apis: [ { id: p, name: P, source: { url: "`+base+`/docs/", type: swagger-ui } } ]`)

	o := check(StatusChanged)
	if !strings.HasSuffix(o.ResolvedURL, "/v1/openapi.json") || !strings.Contains(o.Discovery, "swagger-initializer.js") {
		t.Fatalf("discovery: %+v", o)
	}
	pageHits := site.count("/docs/")
	if o := check(StatusUnchanged); !o.NotModified {
		t.Error("second check should hit the discovered spec with its ETag")
	}
	if site.count("/docs/") != pageHits {
		t.Error("the page must not be fetched again while the discovered spec works")
	}

	// the partner moves the spec: rediscovery from the page
	site.del("/v1/openapi.json")
	site.set("/docs/swagger-initializer.js", `SwaggerUIBundle({ url: "/v2/openapi.json" })`)
	site.set("/v2/openapi.json", specA)
	o = check(StatusUnchanged)
	if !strings.HasSuffix(o.ResolvedURL, "/v2/openapi.json") {
		t.Errorf("rediscovery: %+v", o)
	}
}

func TestHTMLDegradedMode(t *testing.T) {
	site, base := newSite(t, map[string]string{"/api.html": `<h1>Orders</h1><p>GET /orders</p><p>Updated 2026-11-14</p>`})
	_, check := monitorFor(t, `apis: [ { id: h, name: H, source: { url: "`+base+`/api.html", type: html, text_ignore: ["Updated \\S+"] } } ]`)

	check(StatusChanged)
	site.set("/api.html", `<h1>Orders</h1><p>GET /orders</p><p>Updated 2026-12-01</p>`)
	check(StatusUnchanged) // only the ignored date changed
	site.set("/api.html", `<h1>Orders</h1><p>GET /orders?status=x</p><p>Updated 2026-12-02</p>`)
	o := check(StatusChanged)
	if o.Summary.Items[0].RuleID != diff.RuleHTMLChanged || o.Summary.MaxLevel() != diff.LevelWarn {
		t.Errorf("summary: %+v", o.Summary.Items)
	}
}

func TestAutoFallsBackToHTMLPolitely(t *testing.T) {
	site, base := newSite(t, map[string]string{"/doc": `<html><body><h1>API</h1><p>Contact us</p></body></html>`})
	_, check := monitorFor(t, `apis: [ { id: a, name: A, source: { url: "`+base+`/doc", type: auto } } ]`)

	o := check(StatusChanged)
	if o.Spec.Format != "html" || !strings.Contains(o.Discovery, "mode dégradé") {
		t.Fatalf("auto without spec should fall back to html: %+v", o)
	}
	probes := site.count("/openapi.json")
	check(StatusUnchanged)
	if site.count("/openapi.json") != probes {
		t.Error("usual locations must not be probed again once the API is in html mode")
	}
	// the partner finally publishes a spec referenced by the page
	site.set("/doc", `<html><body><redoc spec-url="/openapi.json"></redoc></body></html>`)
	site.set("/openapi.json", specA)
	o = check(StatusChanged)
	if o.Spec.Format != "openapi-3.0" || o.Summary.Items[0].RuleID != "capture-mode-changed" {
		t.Errorf("switch to semantic mode: %+v", o.Summary.Items)
	}
}
