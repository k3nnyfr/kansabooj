package discover

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/fetch"
)

const spec = `{"openapi":"3.0.3","info":{"title":"T","version":"1"},"paths":{}}`

// site serves a set of paths; everything else is 404.
func site(t *testing.T, files map[string]string) (*httptest.Server, *fetch.Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	d := config.Duration(5 * time.Second)
	c, err := fetch.New(config.HTTP{UserAgent: "t", MaxBodyBytes: 1 << 20}, &config.API{ID: "x", Source: config.Source{URL: srv.URL}, Timeout: &d})
	if err != nil {
		t.Fatal(err)
	}
	return srv, c
}

func run(t *testing.T, files map[string]string, page, specName string) (*Result, error) {
	t.Helper()
	srv, c := site(t, files)
	return Discover(context.Background(), c, srv.URL+page, []byte(files[strings.SplitN(page, "?", 2)[0]]), Options{SpecName: specName})
}

func TestSwaggerUIInitializer(t *testing.T) {
	r, err := run(t, map[string]string{
		"/docs/index.html": `<html><body><div id="swagger-ui"></div><script src="./swagger-ui-bundle.js"></script>
			<script src="./swagger-initializer.js" charset="UTF-8"></script></body></html>`,
		"/docs/swagger-initializer.js": `window.onload = function() {
			window.ui = SwaggerUIBundle({ url: "/api/v1/openapi.json", dom_id: '#swagger-ui', oauth2RedirectUrl: "https://x/oauth2-redirect.html" });
		};`,
		"/api/v1/openapi.json": spec,
	}, "/docs/index.html", "")
	if err != nil || !strings.HasSuffix(r.SpecURL, "/api/v1/openapi.json") || !strings.Contains(r.Method, "swagger-initializer.js") {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestSwaggerUIUrlsAndSpecName(t *testing.T) {
	files := map[string]string{
		"/": `<script>const ui = SwaggerUIBundle({
			urls: [ { url: "v1/spec.json", name: "Orders v1" }, { name: "Orders v2", url: "v2/spec.json" } ],
			dom_id: "#swagger-ui" })</script>`,
		"/v1/spec.json": spec,
		"/v2/spec.json": spec,
	}
	r, err := run(t, files, "/", "")
	if err != nil || !strings.HasSuffix(r.SpecURL, "/v1/spec.json") || len(r.Alternatives) != 2 {
		t.Fatalf("first spec by default: %+v %v", r, err)
	}
	r, err = run(t, files, "/", "Orders v2")
	if err != nil || !strings.HasSuffix(r.SpecURL, "/v2/spec.json") {
		t.Fatalf("spec_name: %+v %v", r, err)
	}
	if _, err := run(t, files, "/", "v3"); err == nil || !strings.Contains(err.Error(), "Orders v1, Orders v2") {
		t.Fatalf("unknown spec_name should list the choices: %v", err)
	}
}

func TestSpringdocConfigURL(t *testing.T) {
	r, err := run(t, map[string]string{
		"/swagger-ui/index.html":             `<script src="./swagger-initializer.js"></script>`,
		"/swagger-ui/swagger-initializer.js": `window.ui = SwaggerUIBundle({ configUrl: "/v3/api-docs/swagger-config", dom_id: "#swagger-ui" });`,
		"/v3/api-docs/swagger-config":        `{"configUrl":"/v3/api-docs/swagger-config","url":"/v3/api-docs","validatorUrl":""}`,
		"/v3/api-docs":                       spec,
	}, "/swagger-ui/index.html", "")
	if err != nil || !strings.HasSuffix(r.SpecURL, "/v3/api-docs") || !strings.Contains(r.Method, "swagger-config") {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestRedoc(t *testing.T) {
	r, err := run(t, map[string]string{
		"/reference":    `<!DOCTYPE html><html><body><redoc spec-url="./openapi.yaml"></redoc><script src="https://cdn.redoc.ly/redoc/latest/bundles/redoc.standalone.js"></script></body></html>`,
		"/openapi.yaml": "openapi: 3.0.3\ninfo: {title: T, version: '1'}\npaths: {}\n",
	}, "/reference", "")
	if err != nil || !strings.HasSuffix(r.SpecURL, "/openapi.yaml") {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestHeuristicsSwashbuckle(t *testing.T) {
	r, err := run(t, map[string]string{
		"/swagger/index.html":      `<html>custom page without config</html>`,
		"/swagger/v1/swagger.json": spec,
	}, "/swagger/index.html", "")
	if err != nil || !strings.HasSuffix(r.SpecURL, "/swagger/v1/swagger.json") || !strings.Contains(r.Method, "emplacement usuel") {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestNothingFound(t *testing.T) {
	_, err := run(t, map[string]string{"/": `<html><body>Documentation PDF : <a href="doc.pdf">ici</a></body></html>`}, "/", "")
	if err != ErrNotFound {
		t.Fatalf("got %v", err)
	}
}
