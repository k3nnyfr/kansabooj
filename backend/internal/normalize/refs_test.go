package normalize

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const swagger2Root = `swagger: "2.0"
info: { title: Pets, version: "1" }
paths:
  /pets/{id}:
    get:
      parameters: [ { $ref: "common/params.yaml#/id" } ]
      responses:
        "200": { description: ok, schema: { $ref: "models.yaml#/Pet" } }
definitions:
  Local: { type: string }
`

var swagger2Files = map[string]string{
	"https://p.example/api/common/params.yaml": "id: { name: id, in: path, required: true, type: string }\n",
	// Pet → Owner → Pet: a cycle across files, plus an internal ref of models.yaml
	"https://p.example/api/models.yaml": `Pet:
  type: object
  properties:
    name: { $ref: "#/Name" }
    owner: { $ref: "owners.yaml#/Owner" }
Name: { type: string, maxLength: 50 }
`,
	"https://p.example/api/owners.yaml": `Owner:
  type: object
  properties:
    pets: { type: array, items: { $ref: "models.yaml#/Pet" } }
`,
}

func TestSwagger2ExternalRefs(t *testing.T) {
	fetch := func(_ context.Context, u string) ([]byte, error) {
		if b, ok := swagger2Files[u]; ok {
			return []byte(b), nil
		}
		return nil, errors.New("404 " + u)
	}
	r, err := NormalizeWith([]byte(swagger2Root), Options{Location: "https://p.example/api/swagger.yaml", Fetch: fetch})
	if err != nil {
		t.Fatal(err)
	}
	c := string(r.Canonical)
	if !r.Bundled || r.Format != "swagger-2.0" || r.OperationCount != 1 {
		t.Errorf("result %+v", r)
	}
	for _, want := range []string{`"maxLength": 50`, `"#/components/schemas/Pet"`, `"Local": {`, `"name": "id"`} {
		if !strings.Contains(c, want) {
			t.Errorf("canonical lacks %s:\n%s", want, c)
		}
	}
	if strings.Contains(c, ".yaml") {
		t.Errorf("external ref left in the canonical document:\n%s", c)
	}
	if _, err := Load(r.Canonical); err != nil {
		t.Errorf("not self-contained: %v", err)
	}
	// still refused without a fetcher, and file:// still refused on the server side
	if _, err := Normalize([]byte(swagger2Root), nil); err == nil {
		t.Error("external refs without a fetcher must be reported")
	}
	evil := strings.Replace(swagger2Root, "models.yaml#/Pet", "file:///etc/passwd#/x", 1)
	if _, err := NormalizeWith([]byte(evil), Options{Location: "https://p.example/api/swagger.yaml", Fetch: fetch}); err == nil {
		t.Error("file:// $ref must be refused")
	}
}

// The CLI may read the local files of a multi-file spec, never outside its directory.
func TestLocalRefs(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "spec")
	os.MkdirAll(filepath.Join(spec, "schemas"), 0o755)
	os.WriteFile(filepath.Join(spec, "schemas", "order.yaml"), []byte("type: object\nproperties: { id: { type: string } }\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "secret.yaml"), []byte("type: string\n"), 0o644)
	os.Symlink(filepath.Join(dir, "secret.yaml"), filepath.Join(spec, "link.yaml"))
	root := `openapi: 3.0.3
info: { title: T, version: "1" }
paths:
  /orders:
    get:
      responses:
        "200": { description: ok, content: { application/json: { schema: { $ref: "REF" } } } }
`
	loc := "file://" + filepath.ToSlash(filepath.Join(spec, "openapi.yaml"))
	opts := Options{Location: loc, LocalDir: spec}
	if _, err := NormalizeWith([]byte(strings.Replace(root, "REF", "./schemas/order.yaml", 1)), opts); err != nil {
		t.Errorf("local ref under the spec directory: %v", err)
	}
	for _, ref := range []string{"../secret.yaml", "./link.yaml", "file:///etc/hostname"} {
		_, err := NormalizeWith([]byte(strings.Replace(root, "REF", ref, 1)), opts)
		if err == nil || !strings.Contains(err.Error(), "hors du dossier") && !strings.Contains(err.Error(), "refusée") {
			t.Errorf("%s must be refused, got %v", ref, err)
		}
	}
	// without LocalDir (server), local refs are refused
	if _, err := NormalizeWith([]byte(strings.Replace(root, "REF", "./schemas/order.yaml", 1)), Options{Location: loc, Fetch: func(context.Context, string) ([]byte, error) { return nil, nil }}); err == nil {
		t.Error("file refs without LocalDir must be refused")
	}
}
