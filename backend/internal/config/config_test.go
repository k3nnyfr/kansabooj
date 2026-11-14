package config

import (
	"encoding/json"
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

func TestPublicJSONRoundTrip(t *testing.T) {
	c, _ := Parse([]byte(base))
	a, _ := c.API("petstore")
	var back API
	if err := json.Unmarshal([]byte(a.PublicJSON()), &back); err != nil || back.Interval.String() != "2h" || back.Ignore[0] != "$.servers" {
		t.Errorf("round trip: %+v %v", back, err)
	}
}

func TestDurationString(t *testing.T) {
	for d, want := range map[time.Duration]string{6 * time.Hour: "6h", 90 * time.Minute: "1h30m", 45 * time.Second: "45s", 10 * time.Minute: "10m", 0: "0s"} {
		if got := Duration(d).String(); got != want {
			t.Errorf("%v → %q, want %q", d, got, want)
		}
	}
}

func TestGroupsInheritanceProfilesAndSubscriptions(t *testing.T) {
	c, err := Parse([]byte(`
defaults: { interval: 6h, notify: [team], ignore: [$.servers] }
http:
  proxy: http://proxy.corp:3128
  deny_cidrs: [10.0.0.0/8]
  profiles:
    internal: { proxy: none, allow_cidrs: [10.0.0.0/8], deny_cidrs: [] }
notifiers:
  - { name: team, type: slack, url: "https://hooks.example/a" }
  - { name: security, type: googlechat, url: "https://chat.example/b", subscribe: { tags: [pci] } }
  - { name: internal-chan, type: slack, url: "https://hooks.example/c", subscribe: { groups: [internal] } }
groups:
  - id: internal
    name: APIs internes
    owner: platform
    interval: 15m
    network: internal
    tags: [internal]
    ignore: [$.info.x-build]
    auth: { type: bearer, token: t0k }
apis:
  - id: billing
    name: Billing
    group: internal
    tags: [pci, internal]
    source: { url: "https://billing.svc.cluster.local/openapi.json" }
  - id: stripe
    name: Stripe
    tags: [pci]
    interval: 1h
    source: { url: "https://stripe.example/spec.json" }
`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := c.API("billing")
	if b.Owner != "platform" || b.Interval.String() != "15m" || b.Network != "internal" || b.Auth == nil || b.Auth.Token != "t0k" {
		t.Errorf("group values not inherited: %+v", b)
	}
	if strings.Join(b.Tags, ",") != "internal,pci" || len(b.Ignore) != 2 {
		t.Errorf("tags/ignore merge: %v %v", b.Tags, b.Ignore)
	}
	s, _ := c.API("stripe")
	if s.Network != DefaultProfile || s.Interval.String() != "1h" || s.Group != "" {
		t.Errorf("stripe defaults: %+v", s)
	}

	eff := c.HTTP.ForProfile(b.Network)
	if eff.Proxy != "none" || len(eff.DenyCIDRs) != 0 || eff.AllowCIDRs[0] != "10.0.0.0/8" {
		t.Errorf("internal profile = %+v", eff)
	}
	if d := c.HTTP.ForProfile(s.Network); d.Proxy != "http://proxy.corp:3128" || len(d.DenyCIDRs) != 1 {
		t.Errorf("default profile = %+v", d)
	}

	sec, chat := &c.Notifiers[1], &c.Notifiers[2]
	if !sec.Subscribed(b) || !sec.Subscribed(s) || !chat.Subscribed(b) || chat.Subscribed(s) {
		t.Error("subscriptions mismatch")
	}
}

func TestGroupValidation(t *testing.T) {
	_, err := Parse([]byte(`
http: { profiles: { default: {}, lan: { proxy: "nope" } } }
notifiers:
  - { name: n, type: slack, url: "https://x.example", subscribe: { groups: [ghost] } }
groups:
  - { id: g, name: G, network: wan, tags: ["Bad Tag"], auth: { type: basic } }
  - { id: g, name: G2 }
apis:
  - { id: a, name: A, group: missing, network: lan2, tags: [UPPER], source: { url: "https://a.example" } }
  - { id: b, name: B, group: g, source: { url: "https://b.example" } }
`))
	msg := err.Error()
	for _, want := range []string{"nom réservé", `"none" ou URL`, "groupe \"ghost\" inconnu", "profil réseau \"wan\"", "Bad Tag",
		"id de groupe en double", "groupe \"missing\" non déclaré", "profil réseau \"lan2\"", "UPPER", "username obligatoire"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in:\n%s", want, msg)
		}
	}
	if strings.Count(msg, "username obligatoire") != 1 {
		t.Errorf("inherited auth problem should be reported once:\n%s", msg)
	}
}

func TestDatabaseSection(t *testing.T) {
	t.Setenv("DBPW", "p@ss:w/rd")
	c, err := Parse([]byte(`
database:
  host: pg.dba.example
  name: shared
  user: kansabooj_app
  password: ${DBPW}
  sslrootcert: /etc/kansabooj-db/ca.pem
  schema: kansabooj
  migrate: false
apis: [ { id: a, name: A, source: { url: "https://a.example" } } ]
`))
	if err != nil {
		t.Fatal(err)
	}
	d := c.Database
	if d.SSLMode != "verify-full" || d.Port != 5432 || d.AutoMigrate() || d.MaxOpenConns != 10 {
		t.Errorf("defaults: %+v", d)
	}
	dsn := d.DSN()
	for _, want := range []string{"postgres://kansabooj_app:p%40ss%3Aw%2Frd@pg.dba.example:5432/shared?", "sslmode=verify-full", "sslrootcert=%2Fetc%2Fkansabooj-db%2Fca.pem", "connect_timeout=10"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN %q lacks %q", dsn, want)
		}
	}
	if strings.Contains(d.Redacted(), "p@ss") {
		t.Errorf("password leaked: %s", d.Redacted())
	}

	_, err = Parse([]byte(`
database: { url: "mysql://x", host: h, sslmode: maybe, schema: "Bad-Schema", sslcert: /c, max_open_conns: 2, max_idle_conns: 5 }
apis: [ { id: a, name: A, source: { url: "https://a.example" } } ]
`))
	for _, want := range []string{"exclusifs", "postgres://", "name obligatoire", "user obligatoire", "sslmode", "schema", "sslcert et sslkey", "max_idle_conns", "≥ 4"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestIncludeFragments(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "apis.d"), 0o755)
	write := func(name, body string) { os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600) }
	write("apis.yml", `
include: [apis.d/*.yml]
notifiers: [ { name: team, type: slack, url: "https://h.example" } ]
apis: [ { id: core, name: Core, source: { url: "https://core.example" } } ]
`)
	write("apis.d/paiement.yml", `
groups: [ { id: paiement, name: Paiement, notify: [team] } ]
apis: [ { id: psp, name: PSP, group: paiement, source: { url: "https://psp.example" } } ]
`)
	write("apis.d/crm.yml", `apis: [ { id: crm, name: CRM, source: { url: "https://crm.example" } } ]`)

	c, err := Load(filepath.Join(dir, "apis.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.APIs) != 3 || len(c.Groups) != 1 {
		t.Fatalf("apis=%d groups=%d", len(c.APIs), len(c.Groups))
	}
	if a, _ := c.API("psp"); a.File != "paiement.yml" || a.Notify[0] != "team" {
		t.Errorf("fragment api = %+v", a)
	}

	fp := c.Fingerprint()
	write("apis.d/new.yml", `apis: []`)
	if c.Fingerprint() == fp {
		t.Error("a new fragment must change the fingerprint")
	}

	// a proposed change of a team's fragment, validated against the live configuration
	_, err = LoadWith(filepath.Join(dir, "apis.yml"), map[string][]byte{
		"crm.yml": []byte(`apis: [ { id: psp, name: dup, group: ghost, notify: [nope], source: { url: "https://x.example" } } ]
notifiers: []`),
	})
	for _, want := range []string{"crm.yml : clé inconnue", "id en double (déjà déclaré : crm.yml : api psp)", "groupe \"ghost\"", "notifier \"nope\""} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}
