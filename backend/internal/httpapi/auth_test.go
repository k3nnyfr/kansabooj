package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/scheduler"
	"git.k3nny.fr/kansabooj/internal/store/storetest"
	"git.k3nny.fr/kansabooj/internal/token"
)

func TestAuthScopesAndConfigValidation(t *testing.T) {
	ciTok, ciHash := token.New()
	adminTok, adminHash := token.New()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "apis.d"), 0o755)
	path := filepath.Join(dir, "apis.yml")
	os.WriteFile(path, []byte(`
include: [apis.d/*.yml]
server:
  require_identity: true
  admins: [root]
  tokens:
    - { name: ci, hash: "`+ciHash+`", scopes: [read, validate] }
    - { name: ops, hash: "`+adminHash+`", scopes: [admin] }
notifiers: [ { name: team, type: slack, url: "https://h.example" } ]
apis: [ { id: core, name: Core, source: { url: "https://core.example" } } ]
`), 0o600)
	os.WriteFile(filepath.Join(dir, "apis.d", "paiement.yml"), []byte(`apis: [ { id: psp, name: PSP, source: { url: "https://psp.example" } } ]`), 0o600)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	st := storetest.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sch, _ := scheduler.New(path, cfg, st, log)
	srv := httptest.NewServer(New(sch, st, log).Handler())
	defer srv.Close()

	do := func(method, p, auth, user, body string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+p, strings.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		if user != "" {
			req.Header.Set("X-Forwarded-User", user)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	expect := func(name string, got, want int) {
		t.Helper()
		if got != want {
			t.Errorf("%s: status %d, want %d", name, got, want)
		}
	}

	c, _ := do("GET", "/api/apis", "", "", "")
	expect("anonymous with require_identity", c, 401)
	c, _ = do("GET", "/api/apis", "Bearer kbj_wrong", "", "")
	expect("invalid token", c, 401)
	c, _ = do("GET", "/api/apis", "Bearer "+ciTok, "", "")
	expect("ci token read", c, 200)
	c, body := do("POST", "/api/apis/core/check", "Bearer "+ciTok, "", "")
	expect("ci token without check scope", c, 403)
	if !strings.Contains(body, "token:ci") {
		t.Errorf("403 should name the caller: %s", body)
	}
	c, _ = do("POST", "/api/admin/reload", "", "alice", "")
	expect("sso user is not admin", c, 403)
	c, _ = do("POST", "/api/admin/reload", "", "root", "")
	expect("sso admin", c, 200)
	c, _ = do("POST", "/api/admin/reload", "Bearer "+adminTok, "", "")
	expect("admin token", c, 200)
	c, _ = do("GET", "/api/openapi.yaml", "", "", "")
	expect("public description", c, 200)
	c, body = do("GET", "/api/meta", "", "alice", "")
	if c != 200 || !strings.Contains(body, `"user":"alice"`) {
		t.Errorf("meta for sso user: %d %s", c, body)
	}

	// CI of a team: validate its fragment against the live configuration
	var res struct {
		Valid    bool     `json:"valid"`
		Problems []string `json:"problems"`
		APIs     int      `json:"apis"`
	}
	validate := func(fragment, yaml string) {
		t.Helper()
		c, body := do("POST", "/api/config/validate?fragment="+fragment, "Bearer "+ciTok, "", yaml)
		if c != 200 {
			t.Fatalf("validate: %d %s", c, body)
		}
		res.Problems = nil
		json.Unmarshal([]byte(body), &res)
	}
	validate("paiement.yml", `apis: [ { id: psp, name: PSP v2, notify: [team], source: { url: "https://psp.example/v2" } } ]`)
	if !res.Valid || res.APIs != 2 {
		t.Errorf("valid fragment replacing the live one: %+v", res)
	}
	validate("crm.yml", `apis: [ { id: core, name: Dup, notify: [nope], source: { url: "https://x.example" } } ]`)
	if res.Valid || len(res.Problems) != 2 {
		t.Errorf("invalid new fragment: %+v", res)
	}
	c, _ = do("POST", "/api/config/validate?fragment=../etc/passwd", "Bearer "+ciTok, "", "apis: []")
	expect("path traversal in fragment name", c, 400)
	if _, err := os.Stat(filepath.Join(dir, "apis.d", "crm.yml")); err == nil {
		t.Error("validation must not write anything")
	}
}
