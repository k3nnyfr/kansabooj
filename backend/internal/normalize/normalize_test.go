package normalize

import (
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
