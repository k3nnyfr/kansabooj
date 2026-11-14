// Package normalize turns a raw spec (OpenAPI 3.x or Swagger 2.0, JSON or YAML)
// into a canonical OpenAPI 3 JSON document whose hash only changes when the
// contract changes: keys sorted, Swagger 2 converted, ignored paths removed.
package normalize

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"

	"git.k3nny.fr/kansabooj/internal/config"
)

// ErrNotSpec means the document is not an OpenAPI/Swagger spec (often an HTML page).
var ErrNotSpec = errors.New("le document n'est pas une spécification OpenAPI / Swagger")

type Result struct {
	Format         string // openapi-3.1 | openapi-3.0 | swagger-2.0
	Canonical      []byte // canonical OpenAPI 3 JSON (sorted keys, indented)
	SHA256         string // hash of Canonical
	Version        string // info.version
	Title          string
	OperationCount int
	Doc            *openapi3.T
}

// Normalize parses raw and returns its canonical form. ignore is a list of
// JSONPath-subset expressions ($.a.b, * as key wildcard) removed before hashing.
func Normalize(raw []byte, ignore []string) (*Result, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '<' {
		return nil, fmt.Errorf("%w : page HTML reçue (si c'est une page Swagger-UI / Redoc, déclarer source.type en conséquence)", ErrNotSpec)
	}
	doc, err := decode(trimmed)
	if err != nil {
		return nil, err
	}
	root, ok := doc.(map[string]any)
	if !ok {
		return nil, ErrNotSpec
	}

	res := &Result{}
	switch {
	case strings.HasPrefix(str(root["swagger"]), "2"):
		res.Format = "swagger-2.0"
		if root, err = convertV2(root); err != nil {
			return nil, err
		}
	case strings.HasPrefix(str(root["openapi"]), "3.1"):
		res.Format = "openapi-3.1"
	case strings.HasPrefix(str(root["openapi"]), "3"):
		res.Format = "openapi-3.0"
	default:
		return nil, fmt.Errorf("%w : ni champ \"openapi: 3.x\" ni \"swagger: 2.0\"", ErrNotSpec)
	}

	for _, expr := range ignore {
		path, err := config.ParseIgnorePath(expr)
		if err != nil {
			return nil, fmt.Errorf("ignore %q : %w", expr, err)
		}
		removePath(root, path)
	}

	res.Canonical, err = canonicalJSON(root)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(res.Canonical)
	res.SHA256 = hex.EncodeToString(sum[:])

	res.Doc, err = Load(res.Canonical)
	if err != nil {
		return nil, err
	}
	if res.Doc.Info != nil {
		res.Version, res.Title = res.Doc.Info.Version, res.Doc.Info.Title
	}
	if res.Doc.Paths != nil {
		for _, item := range res.Doc.Paths.Map() {
			res.OperationCount += len(item.Operations())
		}
	}
	return res, nil
}

// Load parses a canonical document produced by Normalize.
func Load(canonical []byte) (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false // multi-file specs: v0.3, through internal/fetch
	doc, err := loader.LoadFromData(canonical)
	if err != nil {
		return nil, fmt.Errorf("spécification illisible : %w", err)
	}
	return doc, nil
}

func decode(b []byte) (any, error) {
	if len(b) > 0 && (b[0] == '{' || b[0] == '[') {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("JSON invalide : %w", err)
		}
		return v, nil
	}
	var v any
	if err := yaml.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("%w : YAML invalide : %v", ErrNotSpec, err)
	}
	return fromYAML(v), nil
}

// fromYAML makes a yaml.v3 tree JSON-compatible: non-string keys (e.g. 200:)
// become strings, timestamps go back to their textual form.
func fromYAML(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = fromYAML(val)
		}
		return t
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[fmt.Sprint(k)] = fromYAML(val)
		}
		return m
	case []any:
		for i := range t {
			t[i] = fromYAML(t[i])
		}
		return t
	case time.Time:
		if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0 {
			return t.Format("2006-01-02")
		}
		return t.Format(time.RFC3339Nano)
	}
	return v
}

func convertV2(root map[string]any) (map[string]any, error) {
	b, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	var v2 openapi2.T
	if err := json.Unmarshal(b, &v2); err != nil {
		return nil, fmt.Errorf("Swagger 2.0 illisible : %w", err)
	}
	v3, err := openapi2conv.ToV3(&v2)
	if err != nil {
		return nil, fmt.Errorf("conversion Swagger 2.0 → OpenAPI 3 : %w", err)
	}
	b, err = json.Marshal(v3)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out map[string]any
	return out, dec.Decode(&out)
}

func canonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ") // encoding/json sorts map keys
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func removePath(node any, path []string) {
	m, ok := node.(map[string]any)
	if !ok || len(path) == 0 {
		return
	}
	key, rest := path[0], path[1:]
	if len(rest) == 0 {
		if key == "*" {
			clear(m)
		} else {
			delete(m, key)
		}
		return
	}
	if key == "*" {
		for _, child := range m {
			removePath(child, rest)
		}
		return
	}
	removePath(m[key], rest)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
