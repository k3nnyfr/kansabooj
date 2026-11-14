package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/scheduler"
	"git.k3nny.fr/kansabooj/internal/store"
	"git.k3nny.fr/kansabooj/internal/store/storetest"
)

const v1 = `{"openapi":"3.0.3","info":{"title":"T","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
const v2 = `{"openapi":"3.0.3","info":{"title":"T","version":"2"},"paths":{}}`

func TestAPIFlow(t *testing.T) {
	body := v1
	partner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
	defer partner.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "apis.yml")
	os.WriteFile(path, []byte(`
server: { language: en }
groups: [ { id: partners, name: Partners, tags: [external] } ]
apis:
  - id: p
    name: Partner
    group: partners
    tags: [pci]
    source: { url: "`+partner.URL+`/spec.json" }
    auth: { type: header, name: X-Key, value: top-secret }
`), 0o600)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st := storetest.Open(t)
	defer st.Close()
	st.SyncAPIs(ctx, cfg.APIs)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sch, err := scheduler.New(path, cfg, st, log)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(sch, st, log).Handler())
	defer srv.Close()

	call := func(method, p string, want int, out any) string {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+p, nil)
		req.Header.Set("X-Forwarded-User", "alice")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, p, resp.StatusCode, want, b)
		}
		if out != nil {
			json.Unmarshal(b, out)
		}
		return string(b)
	}

	var meta map[string]any
	call("GET", "/api/meta", 200, &meta)
	if meta["language"] != "en" || meta["user"] != "alice" || len(meta["groups"].([]any)) != 1 || len(meta["tags"].([]any)) != 2 {
		t.Errorf("meta = %v", meta)
	}
	call("POST", "/api/apis/p/check", 200, nil)
	body = v2
	var res map[string]any
	call("POST", "/api/apis/p/check", 200, &res)
	if res["status"] != "changed" {
		t.Fatalf("check = %v", res)
	}
	changeID := int(res["change_id"].(float64))

	var list []store.APIRow
	call("GET", "/api/apis", 200, &list)
	if len(list) != 1 || list[0].Unacknowledged != 1 || list[0].UnackedBreaking != 1 {
		t.Errorf("list = %+v", list)
	}
	detail := call("GET", "/api/apis/p", 200, nil)
	if strings.Contains(detail, "top-secret") {
		t.Error("secret leaked in API detail")
	}
	if list[0].Group != "partners" || strings.Join(list[0].Tags, ",") != "external,pci" {
		t.Errorf("group/tags = %q %v", list[0].Group, list[0].Tags)
	}
	call("GET", "/api/apis?tag=pci&group=partners", 200, &list)
	if len(list) != 1 {
		t.Errorf("filter should match: %d", len(list))
	}
	call("GET", "/api/apis?tag=nope", 200, &list)
	if len(list) != 0 {
		t.Errorf("filter should exclude: %d", len(list))
	}
	call("GET", "/readyz", 200, nil)
	var tl []store.TimelineEntry
	call("GET", "/api/apis/p/timeline", 200, &tl)
	if len(tl) != 2 {
		t.Fatalf("timeline = %+v", tl)
	}
	var ch store.ChangeDetail
	call("GET", "/api/changes/"+itoa(changeID), 200, &ch)
	if len(ch.Items) == 0 {
		t.Error("change without items")
	}
	call("POST", "/api/changes/"+itoa(changeID)+"/ack", 204, nil)
	call("GET", "/api/changes/"+itoa(changeID), 200, &ch)
	if ch.AcknowledgedBy != "alice" {
		t.Errorf("ack by = %q", ch.AcknowledgedBy)
	}
	if s := call("GET", "/api/snapshots/"+itoa(int(ch.ToSnapshotID))+"/spec?form=raw", 200, nil); s != v2 {
		t.Errorf("raw spec = %q", s)
	}
	call("GET", "/api/changes/abc", 400, nil)
	call("GET", "/api/changes/999", 404, nil)
	call("POST", "/api/apis/nope/check", 404, nil)
	call("GET", "/api/unknown", 404, nil)
	call("POST", "/api/admin/reload", 200, nil)
	if !strings.Contains(call("GET", "/apis/p", 200, nil), "Kansabooj") {
		t.Error("SPA fallback not served")
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
