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

func TestJournal(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	defer st.Close()
	apis := []config.API{
		{ID: "a", Name: "A", Source: config.Source{URL: "https://a.example", Type: "openapi"}},
		{ID: "b", Name: "B", Source: config.Source{URL: "https://b.example", Type: "openapi"}},
	}
	if err := st.SyncAPIs(ctx, apis); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	at := func(m int) time.Time { return base.Add(time.Duration(m) * time.Minute) }

	// Two versions of A (manual checks by alice, then scheduled), a scheduled check of B in error.
	v1, err := st.SaveNewVersion(ctx, store.NewVersion{
		Check: store.CheckRecord{APIID: "a", StartedAt: at(1), Trigger: "manual", Actor: "alice", Status: "changed"},
		Raw:   []byte("1"), RawMedia: "application/json", Normalized: []byte("1"),
		Snapshot: store.Snapshot{Format: "openapi-3.0", Version: "1"}, Semantic: true,
		Summary: &diff.Summary{VersionTo: "1", Items: []diff.Item{{RuleID: "first-capture", Level: "info", InScope: true, Text: "x"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	c1, _ := st.GetChange(ctx, v1)
	v2, err := st.SaveNewVersion(ctx, store.NewVersion{
		Check: store.CheckRecord{APIID: "a", StartedAt: at(2), Trigger: "schedule", Status: "changed"},
		Raw:   []byte("2"), RawMedia: "application/json", Normalized: []byte("2"),
		Snapshot: store.Snapshot{Format: "openapi-3.0", Version: "2"}, PrevSnapID: &c1.ToSnapshotID, Semantic: true,
		Summary: &diff.Summary{VersionFrom: "1", VersionTo: "2", Infos: 1, Items: []diff.Item{{RuleID: "r", Level: "info", InScope: true, Text: "y"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordError(ctx, store.CheckRecord{APIID: "b", StartedAt: at(3), Trigger: "schedule", Status: "error", ErrorKind: "http", Error: "HTTP 500"}, 5); err != nil {
		t.Fatal(err)
	}
	// "Acknowledge all": every change of A gets the same timestamp.
	if n, err := st.AcknowledgeAll(ctx, "a", "bob"); err != nil || n != 2 {
		t.Fatalf("ack all = %d, %v", n, err)
	}
	n := &store.Notification{ChangeID: &v2, APIID: "a", Notifier: "chat", Kind: "change", Payload: []byte("{}")}
	if ok, err := st.CreateNotification(ctx, n); !ok || err != nil {
		t.Fatalf("notification: %v, %v", ok, err)
	}
	if err := st.MarkNotification(ctx, n.ID, "", nil); err != nil {
		t.Fatal(err)
	}

	all, next, err := st.Journal(ctx, store.JournalQuery{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if !next.IsZero() || len(all) != 6 {
		t.Fatalf("journal = %d entrées, next %v : %+v", len(all), next, all)
	}
	kinds := map[string]int{}
	for i, e := range all {
		kinds[e.Kind]++
		if i > 0 && e.At.After(all[i-1].At) {
			t.Errorf("ordre : %v après %v", e.At, all[i-1].At)
		}
		if e.APIName == "" {
			t.Errorf("nom d'API manquant : %+v", e)
		}
	}
	if kinds["check"] != 3 || kinds["ack"] != 2 || kinds["notification"] != 1 {
		t.Errorf("types = %v", kinds)
	}
	for _, e := range all {
		switch {
		case e.Kind == "check" && e.Trigger == "manual" && e.Actor != "alice":
			t.Errorf("auteur du check manuel : %+v", e)
		case e.Kind == "check" && e.Trigger == "schedule" && e.Actor != "":
			t.Errorf("check planifié avec auteur : %+v", e)
		case e.Kind == "ack" && (e.Actor != "bob" || e.ChangeID == 0 || e.Status != "acknowledged"):
			t.Errorf("acquittement : %+v", e)
		case e.Kind == "notification" && (e.Status != "sent" || e.Notifier != "chat" || e.ChangeID != v2):
			t.Errorf("notification : %+v", e)
		case e.Kind == "check" && e.APIID == "b" && (e.Status != "error" || e.Error != "HTTP 500"):
			t.Errorf("check en erreur : %+v", e)
		}
	}

	only, _, _ := st.Journal(ctx, store.JournalQuery{Limit: 50, Kinds: []string{store.JournalManualCheck}})
	if len(only) != 1 || only[0].Actor != "alice" {
		t.Errorf("filtre check_manual : %+v", only)
	}
	sched, _, _ := st.Journal(ctx, store.JournalQuery{Limit: 50, Kinds: []string{store.JournalScheduledCheck}, APIID: "b"})
	if len(sched) != 1 || sched[0].APIID != "b" {
		t.Errorf("filtre check_scheduled + api : %+v", sched)
	}

	// Pages of 3, newest first: notification, then the two tied acknowledgments, which must stay
	// together on the next page; nothing is lost or repeated across pages.
	seen := 0
	var before time.Time
	for page := 0; page < 10; page++ {
		got, nxt, err := st.Journal(ctx, store.JournalQuery{Limit: 3, Before: before})
		if err != nil {
			t.Fatal(err)
		}
		acks := 0
		for _, e := range got {
			if e.Kind == "ack" {
				acks++
			}
		}
		if acks == 1 {
			t.Errorf("page %d sépare les acquittements simultanés : %+v", page, got)
		}
		seen += len(got)
		if nxt.IsZero() {
			break
		}
		before = nxt
	}
	if seen != 6 {
		t.Errorf("pagination : %d entrées vues, 6 attendues", seen)
	}
}
