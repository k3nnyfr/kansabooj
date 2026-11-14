package httpapi

import (
	"context"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// The published description must stay valid and cover every route.
func TestOpenAPICoversRoutes(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData(openapiSpec)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("invalid OpenAPI: %v", err)
	}
	for _, rt := range (&Server{}).routes() {
		item := doc.Paths.Find(rt.path)
		if item == nil || item.GetOperation(rt.method) == nil {
			t.Errorf("%s %s missing from openapi.yaml", rt.method, rt.path)
		}
		if rt.scope != "" && !strings.Contains(item.GetOperation(rt.method).Summary+item.GetOperation(rt.method).Description, "scope "+rt.scope) && rt.scope != "read" {
			t.Errorf("%s %s: summary should mention its scope %q", rt.method, rt.path, rt.scope)
		}
	}
}
