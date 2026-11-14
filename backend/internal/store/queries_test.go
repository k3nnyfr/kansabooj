package store_test

import (
	"context"
	"testing"
	"time"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/diff"
	"git.k3nny.fr/kansabooj/internal/store"
	"git.k3nny.fr/kansabooj/internal/store/storetest"
)

func TestQueries(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	defer st.Close()
	apis := []config.API{{ID: "a", Name: "A", Source: config.Source{URL: "https://a.example", Type: "openapi"}}}
	if err := st.SyncAPIs(ctx, apis); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	save := func(body string, prev *int64, sm *diff.Summary) int64 {
		id, err := st.SaveNewVersion(ctx, store.NewVersion{
			Check: store.CheckRecord{APIID: "a", StartedAt: at, Trigger: "manual", Status: "changed"},
			Raw:   []byte(body), RawMedia: "application/json", Normalized: []byte(body),
			Snapshot: store.Snapshot{Format: "openapi-3.0", Version: body}, PrevSnapID: prev, Summary: sm, Semantic: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := save("1", nil, &diff.Summary{VersionTo: "1", Infos: 1, Items: []diff.Item{{RuleID: "first-capture", Level: "info", InScope: true, Text: "x"}}})
	c1, _ := st.GetChange(ctx, first)
	second := save("2", &c1.ToSnapshotID, &diff.Summary{VersionFrom: "1", VersionTo: "2", Errors: 1, InScope: 1,
		Items: []diff.Item{{RuleID: "r", Level: "error", Method: "GET", Path: "/x", InScope: true, Text: "removed"}}})

	list, err := st.ListAPIs(ctx, false)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	r := list[0]
	if r.Version != "2" || r.Unacknowledged != 1 || r.UnackedBreaking != 1 || r.ChangeCount != 2 || r.LastCheckedAt == nil || r.LastChangeAt == nil {
		t.Errorf("unexpected row %+v", r)
	}
	if !r.LastCheckedAt.Equal(at) {
		t.Errorf("time round trip: %v != %v", r.LastCheckedAt, at)
	}
	tl, err := st.TimelinePage(ctx, "a", 10, 0)
	if err != nil || len(tl) != 2 || tl[0].ID != second || !tl[1].FirstCapture || tl[0].InScopeErrors != 1 {
		t.Fatalf("timeline = %+v, %v", tl, err)
	}
	if page, _ := st.TimelinePage(ctx, "a", 10, second); len(page) != 1 {
		t.Errorf("pagination: %+v", page)
	}
	if err := st.Acknowledge(ctx, second, "alice"); err != nil {
		t.Fatal(err)
	}
	d, _ := st.GetChange(ctx, second)
	if d.AcknowledgedBy != "alice" || d.AcknowledgedAt == nil || len(d.Items) != 1 {
		t.Errorf("detail = %+v", d)
	}
	if r, _ := st.GetAPI(ctx, "a"); r.Unacknowledged != 0 {
		t.Errorf("still unacknowledged: %+v", r)
	}
	if _, body, _, err := st.SnapshotSpec(ctx, d.ToSnapshotID, true); err != nil || string(body) != "2" {
		t.Errorf("spec = %q, %v", body, err)
	}

	n := &store.Notification{ChangeID: &second, APIID: "a", Notifier: "chat", Kind: "change", Payload: []byte("{}")}
	if ok, err := st.CreateNotification(ctx, n); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := st.CreateNotification(ctx, n); ok {
		t.Error("duplicate notification created")
	}
	due, _ := st.DueNotifications(ctx, time.Now().Add(time.Second))
	if len(due) != 1 {
		t.Fatalf("due = %+v", due)
	}
	st.MarkNotification(ctx, due[0].ID, "", nil)
	if due, _ := st.DueNotifications(ctx, time.Now().Add(time.Second)); len(due) != 0 {
		t.Errorf("sent notification still due")
	}
	next := time.Now().Add(time.Hour)
	if err := st.SetNextCheck(ctx, "a", next); err != nil {
		t.Fatal(err)
	}
	if m, _ := st.NextChecks(ctx); m["a"].Unix() != next.Unix() {
		t.Errorf("next checks = %v", m)
	}
}
