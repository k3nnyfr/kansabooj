package diff

import (
	"testing"

	"git.k3nny.fr/kansabooj/internal/normalize"
)

const before = `openapi: 3.0.3
info: { title: Orders, version: "1.0" }
paths:
  /orders:
    get:
      parameters:
        - { name: status, in: query, schema: { type: string } }
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
                  amount: { type: number }
  /orders/{orderId}:
    delete:
      parameters: [{ name: orderId, in: path, required: true, schema: { type: string } }]
      responses: { "204": { description: deleted } }
`

// response property removed (breaking), new required query param (breaking),
// DELETE removed out of scope, version bumped.
const after = `openapi: 3.0.3
info: { title: Orders, version: "1.1" }
paths:
  /orders:
    get:
      parameters:
        - { name: status, in: query, schema: { type: string } }
        - { name: tenant, in: query, required: true, schema: { type: string } }
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
`

func mustLoad(t *testing.T, s string) *normalize.Result {
	t.Helper()
	r, err := normalize.Normalize([]byte(s), nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCompareClassifiesAndScopes(t *testing.T) {
	b, a := mustLoad(t, before), mustLoad(t, after)
	s, err := Compare(b.Doc, a.Doc, []string{"GET /orders", "POST /orders"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Errors < 2 {
		t.Errorf("expected at least 2 breaking changes, got %d: %+v", s.Errors, s.Items)
	}
	var sawVersion, deleteOutOfScope bool
	for _, it := range s.Items {
		t.Logf("%-5s in_scope=%-5v %-6s %-20s %s (%s)", it.Level, it.InScope, it.Method, it.Path, it.Text, it.RuleID)
		if it.RuleID == RuleVersionChanged {
			sawVersion = true
		}
		if it.Path == "/orders/{orderId}" && !it.InScope {
			deleteOutOfScope = true
		}
		if it.Path == "/orders" && it.Method == "GET" && !it.InScope {
			t.Errorf("GET /orders change should be in scope: %+v", it)
		}
	}
	if !sawVersion {
		t.Error("version change not reported")
	}
	if !deleteOutOfScope {
		t.Error("DELETE /orders/{orderId} removal should be out of scope")
	}
	if s.MaxLevel() != LevelError {
		t.Errorf("max level = %s", s.MaxLevel())
	}
}

func TestNonSemanticChange(t *testing.T) {
	b := mustLoad(t, before)
	a := mustLoad(t, before+"x-note: generated\n")
	s, err := Compare(b.Doc, a.Doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Items) != 1 || s.Items[0].RuleID != RuleNonSemantic {
		t.Errorf("expected a single non-semantic item, got %+v", s.Items)
	}
}

func TestScopeIgnoresParamNamesAndSupportsWildcard(t *testing.T) {
	sc := newScope([]string{"GET /orders/{id}", "POST /payments*"})
	cases := map[[2]string]bool{
		{"GET", "/orders/{orderId}"}:             true,
		{"DELETE", "/orders/{orderId}"}:          false,
		{"POST", "/payments/{paymentId}/cancel"}: true,
		{"GET", "/payments"}:                     false,
		{"", ""}:                                 false,
	}
	for in, want := range cases {
		if got := sc.contains(in[0], in[1]); got != want {
			t.Errorf("contains(%v) = %v, want %v", in, got, want)
		}
	}
}
