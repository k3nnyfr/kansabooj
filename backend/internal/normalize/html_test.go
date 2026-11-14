package normalize

import (
	"strings"
	"testing"
)

const pageV1 = `<!doctype html><html><head><title>Partner Y API</title><style>.x{}</style></head><body>
<nav>Home | Login</nav>
<h1>Orders API</h1><p>GET /orders returns   the list
of orders.</p><ul><li>status (optional)</li></ul>
<script>var csrf="abc";</script><footer>Generated 2026-11-14 10:00</footer>
<p>Last update: 2026-11-14</p></body></html>`

func TestHTMLTextIgnoresChromeAndNoise(t *testing.T) {
	a, err := HTMLText([]byte(pageV1), []string{`Last update: \S+`})
	if err != nil {
		t.Fatal(err)
	}
	want := "Orders API\nGET /orders returns the list of orders.\nstatus (optional)\n"
	if string(a.Canonical) != want || a.Title != "Partner Y API" || a.Format != FormatHTML {
		t.Fatalf("got %q (title %q)", a.Canonical, a.Title)
	}
	// only ignored / non-visible parts change → same hash
	v2 := strings.NewReplacer("2026-11-14", "2026-12-01", `csrf="abc"`, `csrf="xyz"`).Replace(pageV1)
	b, _ := HTMLText([]byte(v2), []string{`Last update: \S+`})
	if a.SHA256 != b.SHA256 {
		t.Errorf("noise changed the hash:\n%s\n%s", a.Canonical, b.Canonical)
	}
	if _, err := HTMLText([]byte(pageV1), []string{"("}); err == nil {
		t.Error("invalid regexp must be reported")
	}
}
