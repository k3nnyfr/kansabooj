package normalize

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

const specJSON = `{"openapi":"3.0.3","info":{"title":"Pets","version":"1.0.0"},
"servers":[{"url":"https://prod.example"}],
"paths":{"/pets":{"get":{"responses":{"200":{"description":"ok"}}},"post":{"responses":{"201":{"description":"created"}}}}}}`

const specYAMLReordered = `
paths:
  /pets:
    post:
      responses:
        201: { description: created }
    get:
      responses:
        200:
          description: ok
servers:
  - url: https://staging.example
info: { version: 1.0.0, title: Pets }
openapi: 3.0.3
`

func TestJSONAndReorderedYAMLHashTheSame(t *testing.T) {
	a, err := Normalize([]byte(specJSON), []string{"$.servers"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Normalize([]byte(specYAMLReordered), []string{"$.servers"})
	if err != nil {
		t.Fatal(err)
	}
	if a.SHA256 != b.SHA256 {
		t.Errorf("hash differs:\n%s\n---\n%s", a.Canonical, b.Canonical)
	}
	if a.Format != "openapi-3.0" || a.Version != "1.0.0" || a.OperationCount != 2 {
		t.Errorf("unexpected result %+v", a)
	}
}

func TestServersMatterWithoutIgnore(t *testing.T) {
	a, _ := Normalize([]byte(specJSON), nil)
	b, _ := Normalize([]byte(specYAMLReordered), nil)
	if a.SHA256 == b.SHA256 {
		t.Error("servers differ, hash should differ when not ignored")
	}
}

func TestWildcardIgnore(t *testing.T) {
	r, err := Normalize([]byte(specJSON), []string{"$.paths.*.post"})
	if err != nil {
		t.Fatal(err)
	}
	if r.OperationCount != 1 {
		t.Errorf("post should have been removed, got %d ops", r.OperationCount)
	}
}

func TestSwagger2IsConverted(t *testing.T) {
	raw, err := os.ReadFile("testdata/swagger2.yaml")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Normalize(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != "swagger-2.0" || !strings.Contains(string(r.Canonical), `"openapi": "3`) {
		t.Errorf("not converted: format=%s\n%s", r.Format, r.Canonical)
	}
	if r.OperationCount != 2 {
		t.Errorf("ops = %d", r.OperationCount)
	}
}

func TestHTMLIsRejected(t *testing.T) {
	_, err := Normalize([]byte("<!DOCTYPE html><html><body>Swagger UI</body></html>"), nil)
	if !errors.Is(err, ErrNotSpec) {
		t.Errorf("expected ErrNotSpec, got %v", err)
	}
}

func TestExternalRefsAreBundled(t *testing.T) {
	files := map[string]string{
		"https://p.example/api/schemas/order.yaml": "type: object\nrequired: [id]\nproperties:\n  id: { type: string }\n",
	}
	root := `openapi: 3.0.3
info: { title: T, version: "1" }
paths:
  /orders:
    get:
      responses:
        "200":
          description: ok
          content: { application/json: { schema: { $ref: "./schemas/order.yaml" } } }
`
	var asked []string
	fetchFn := func(_ context.Context, u string) ([]byte, error) {
		asked = append(asked, u)
		if b, ok := files[u]; ok {
			return []byte(b), nil
		}
		return nil, errors.New("404")
	}
	r, err := NormalizeWith([]byte(root), Options{Location: "https://p.example/api/openapi.yaml", Fetch: fetchFn})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Bundled || strings.Contains(string(r.Canonical), "order.yaml\"") && !strings.Contains(string(r.Canonical), "#/components/schemas/") {
		t.Errorf("not internalized:\n%s", r.Canonical)
	}
	if _, err := Load(r.Canonical); err != nil {
		t.Errorf("canonical document must be self-contained: %v", err)
	}
	// a change in the referenced file changes the hash
	files["https://p.example/api/schemas/order.yaml"] += "  amount: { type: number }\n"
	r2, _ := NormalizeWith([]byte(root), Options{Location: "https://p.example/api/openapi.yaml", Fetch: fetchFn})
	if r2.SHA256 == r.SHA256 {
		t.Error("change in an external file must change the hash")
	}

	// local files and other schemes are refused
	evil := strings.Replace(root, "./schemas/order.yaml", "file:///etc/passwd", 1)
	if _, err := NormalizeWith([]byte(evil), Options{Location: "https://p.example/api/openapi.yaml", Fetch: fetchFn}); err == nil {
		t.Error("file:// $ref must be refused")
	}
	for _, u := range asked {
		if !strings.HasPrefix(u, "https://") {
			t.Errorf("non-http location reached the fetcher: %s", u)
		}
	}
	if _, err := Normalize([]byte(root), nil); err == nil {
		t.Error("external refs without a fetcher must be reported")
	}
	if !IsSpec([]byte(root)) || IsSpec([]byte("<html>")) {
		t.Error("IsSpec")
	}
}
