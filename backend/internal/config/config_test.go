package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const base = `
defaults:
  interval: 2h
  notify: [chat]
  ignore: [$.servers]
notifiers:
  - name: chat
    type: googlechat
    url: https://chat.example/hook
apis:
  - id: petstore
    name: Petstore
    source: { url: "https://petstore.example/openapi.json", type: openapi }
`

func TestParseDefaults(t *testing.T) {
	c, err := Parse([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := c.API("petstore")
	if a.Interval.Std() != 2*time.Hour {
		t.Errorf("interval = %v", a.Interval.Std())
	}
	if a.Timeout.Std() != 30*time.Second {
		t.Errorf("timeout = %v", a.Timeout.Std())
	}
	if len(a.Notify) != 1 || a.Notify[0] != "chat" {
		t.Errorf("notify = %v", a.Notify)
	}
	if c.Notifiers[0].MinLevel != "warn" || !*c.Notifiers[0].OnlyInScope {
		t.Errorf("notifier defaults not applied: %+v", c.Notifiers[0])
	}
}

func TestExpansion(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(secret, []byte("s3cr#t: \"x\n"), 0o600)
	t.Setenv("KEY", "abc: #not-a-comment")

	c, err := Parse([]byte(base + `
  - id: protected
    name: Protected
    source: { url: "https://p.example/spec.yaml" }
    auth:
      type: oauth2_client_credentials
      token_url: https://auth.example/token
      client_id: ${KEY}
      client_secret: ${file:` + secret + `}
      params: { audience: "${UNSET:-default-aud}" }
`))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := c.API("protected")
	if a.Auth.ClientID != "abc: #not-a-comment" {
		t.Errorf("client_id = %q", a.Auth.ClientID)
	}
	if a.Auth.ClientSecret != `s3cr#t: "x` {
		t.Errorf("client_secret = %q", a.Auth.ClientSecret)
	}
	if a.Auth.Params["audience"] != "default-aud" {
		t.Errorf("audience = %q", a.Auth.Params["audience"])
	}
	if strings.Contains(a.PublicJSON(), "abc") {
		t.Errorf("PublicJSON leaks secret: %s", a.PublicJSON())
	}
}

func TestValidationCollectsAllProblems(t *testing.T) {
	_, err := Parse([]byte(`
notifiers:
  - { name: n, type: discord, url: "nope" }
apis:
  - id: Bad_ID
    name: x
    source: { url: "ftp://x" }
    notify: [missing]
    gateway: { exposed_operations: ["get /x"] }
    auth: { type: header, name: X-Key }
  - id: dup
    name: a
    source: { url: "https://a.example" }
    interval: 1h
  - id: dup
    name: b
    source: { url: "https://b.example", type: graphql }
    ignore: ["servers"]
    client: ${NOT_DEFINED_VAR}
`))
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	for _, want := range []string{"NOT_DEFINED_VAR", "discord", "url absente", "id invalide", "source.url", "missing",
		"exposed_operations", "value obligatoire", "id en double", "graphql", "ignore", "field client not found"} {
		if !strings.Contains(ve.Error(), want) {
			t.Errorf("missing problem mentioning %q in:\n%s", want, ve.Error())
		}
	}
}

func TestOrigin(t *testing.T) {
	a := API{Source: Source{URL: "https://Docs.example/a/b?x=1"}}
	if got := a.Origin(); got != "https://docs.example:443" {
		t.Errorf("origin = %q", got)
	}
}
