package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/diff"
	"git.k3nny.fr/kansabooj/internal/monitor"
	"git.k3nny.fr/kansabooj/internal/store"
	"git.k3nny.fr/kansabooj/internal/store/storetest"
)

var items = []diff.Item{
	{RuleID: "r1", Level: "error", Method: "GET", Path: "/orders", Text: "removed property", InScope: true},
	{RuleID: "r2", Level: "error", Method: "DELETE", Path: "/admin", Text: "path removed", InScope: false},
	{RuleID: "r3", Level: "info", Text: "version changed", InScope: true},
}

func notifier(min string, onlyInScope bool) *config.Notifier {
	return &config.Notifier{Name: "n", Type: "slack", MinLevel: min, OnlyInScope: &onlyInScope, Language: "fr"}
}

func TestSelectFor(t *testing.T) {
	ev := Event{Kind: KindChange, Items: items}
	e, ok := selectFor(ev, notifier("error", true))
	if !ok || len(e.Items) != 2 || e.OmittedOutOfScope != 1 || e.counts[0] != 1 {
		t.Errorf("in-scope breaking should pass: ok=%v %+v", ok, e)
	}
	if _, ok := selectFor(Event{Kind: KindChange, Items: items[1:]}, notifier("error", true)); ok {
		t.Error("only out-of-scope breaking + info must not reach an error/in-scope notifier")
	}
	if e, ok := selectFor(Event{Kind: KindChange, Items: items[1:]}, notifier("warn", false)); !ok || e.MaxLevel != "error" {
		t.Error("out-of-scope breaking must reach a notifier with only_in_scope=false")
	}
	if _, ok := selectFor(Event{Kind: KindStale}, notifier("error", true)); !ok {
		t.Error("stale always goes through")
	}
}

func TestPayloadsAreValidAndLocalized(t *testing.T) {
	e, _ := selectFor(Event{Kind: KindChange, APIName: "Partner X", APIID: "px", ChangeID: 7, VersionFrom: "1", VersionTo: "2",
		Items: items, Link: "https://k.example/apis/px?change=7", Owner: "team"}, notifier("info", true))
	for _, typ := range []string{"slack", "googlechat", "teams", "generic"} {
		for lang, want := range map[string]string{"fr": "cassant", "en": "breaking"} {
			b, err := payload(typ, lang, &e)
			if err != nil {
				t.Fatal(err)
			}
			var v map[string]any
			if err := json.Unmarshal(b, &v); err != nil {
				t.Fatalf("%s: invalid json", typ)
			}
			if !strings.Contains(strings.ToLower(string(b)), want) {
				t.Errorf("%s/%s: missing %q in %s", typ, lang, want, b)
			}
			if !strings.Contains(string(b), "https://k.example/apis/px?change=7") {
				t.Errorf("%s: missing link", typ)
			}
		}
	}
}

func TestDispatcherSendsSignsAndRetries(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	var sigs []string
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		b, _ := io.ReadAll(r.Body)
		bodies, sigs = append(bodies, b), append(sigs, r.Header.Get("X-Kansabooj-Signature"))
		if fail {
			fail = false
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer srv.Close()

	cfg, err := config.Parse([]byte(`
server: { base_url: "https://k.example/" }
defaults: { error_threshold: 2 }
notifiers:
  - { name: hook, type: generic, url: "` + srv.URL + `", secret: s3, min_level: warn }
apis:
  - { id: px, name: Partner X, source: { url: "https://px.example/spec.json" }, notify: [hook] }
`))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st := storetest.Open(t)
	defer st.Close()
	st.SyncAPIs(ctx, cfg.APIs)
	cur := time.Now()
	d, err := NewDispatcher(st, func() *config.Config { return cfg }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	d.now = func() time.Time { return cur }
	api, _ := cfg.API("px")

	changeID, _ := st.SaveNewVersion(ctx, store.NewVersion{Check: store.CheckRecord{APIID: "px", StartedAt: cur, Trigger: "t", Status: "changed"},
		Raw: []byte("x"), Normalized: []byte("x"), RawMedia: "application/json", Summary: &diff.Summary{Items: items}, Semantic: true})
	out := &monitor.Outcome{Status: monitor.StatusChanged, ChangeID: changeID, Summary: &diff.Summary{VersionFrom: "1", VersionTo: "2", Items: items}}
	d.HandleOutcome(ctx, api, out)
	d.HandleOutcome(ctx, api, out) // idempotent: same change, same notifier
	if len(bodies) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(bodies))
	}
	if sigs[0] != Sign("s3", bodies[0]) {
		t.Error("bad HMAC signature")
	}
	if !strings.Contains(string(bodies[0]), `"link":"https://k.example/apis/px?change=`) {
		t.Errorf("link: %s", bodies[0])
	}

	// first attempt failed with 502: retried once the backoff has elapsed
	cur = cur.Add(2 * time.Minute)
	due, _ := st.DueNotifications(ctx, cur)
	if len(due) != 1 {
		t.Fatalf("due = %d", len(due))
	}
	d.attempt(ctx, &cfg.Notifiers[0], &due[0])
	if due, _ := st.DueNotifications(ctx, cur.Add(time.Hour)); len(due) != 0 || len(bodies) != 2 {
		t.Errorf("retry not completed: due=%d bodies=%d", len(due), len(bodies))
	}

	// stale at threshold, then recovered
	d.HandleOutcome(ctx, api, &monitor.Outcome{Status: monitor.StatusError, Err: errors.New("boom"), ConsecutiveErrors: 1})
	d.HandleOutcome(ctx, api, &monitor.Outcome{Status: monitor.StatusError, Err: errors.New("boom"), ConsecutiveErrors: 2})
	d.HandleOutcome(ctx, api, &monitor.Outcome{Status: monitor.StatusUnchanged, PrevStatus: "stale"})
	if len(bodies) != 4 || !strings.Contains(string(bodies[2]), `"kind":"stale"`) || !strings.Contains(string(bodies[3]), `"kind":"recovered"`) {
		t.Errorf("stale/recovered not sent: %d bodies", len(bodies))
	}
}

func TestRecipientsIncludeSubscriptions(t *testing.T) {
	cfg, err := config.Parse([]byte(`
notifiers:
  - { name: team, type: slack, url: "https://h.example/1" }
  - { name: security, type: slack, url: "https://h.example/2", subscribe: { tags: [pci] } }
  - { name: platform, type: slack, url: "https://h.example/3", subscribe: { groups: [internal] } }
groups: [ { id: internal, name: Internal, notify: [] } ]
apis:
  - { id: a, name: A, notify: [team], tags: [pci], source: { url: "https://a.example" } }
  - { id: b, name: B, group: internal, source: { url: "https://b.example" } }
`))
	if err != nil {
		t.Fatal(err)
	}
	names := func(id string) string {
		a, _ := cfg.API(id)
		var out []string
		for _, n := range recipients(cfg, a) {
			out = append(out, n.Name)
		}
		return strings.Join(out, ",")
	}
	if got := names("a"); got != "team,security" {
		t.Errorf("a → %s", got)
	}
	if got := names("b"); got != "platform" {
		t.Errorf("b → %s (group notify: [] must override defaults)", got)
	}
}
