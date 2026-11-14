package config

import (
	"strings"
	"testing"
	"time"
)

func TestDigestLastDue(t *testing.T) {
	paris, _ := time.LoadLocation("Europe/Paris")
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, paris)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	daily := &Digest{Every: "daily", At: "08:30", Timezone: "Europe/Paris"}
	weekly := &Digest{Every: "weekly", At: "09:00", Day: "monday", Timezone: "Europe/Paris"}
	for _, c := range []struct {
		d        *Digest
		now, due string
	}{
		{daily, "2026-11-14 10:00", "2026-11-14 08:30"},
		{daily, "2026-11-14 08:29", "2026-11-13 08:30"},
		{daily, "2026-11-14 08:30", "2026-11-14 08:30"},
		{weekly, "2026-11-14 10:00", "2026-11-09 09:00"}, // samedi → lundi précédent
		{weekly, "2026-11-16 09:00", "2026-11-16 09:00"}, // lundi, pile à l'heure
		{weekly, "2026-11-16 08:59", "2026-11-09 09:00"},
		{daily, "2026-03-29 12:00", "2026-03-29 08:30"}, // passage à l'heure d'été
	} {
		if got := c.d.LastDue(at(c.now).UTC()); !got.Equal(at(c.due)) {
			t.Errorf("%s %s : %v, attendu %s", c.d.Every, c.now, got.In(paris), c.due)
		}
	}
}

func TestDigestValidation(t *testing.T) {
	_, err := Parse([]byte(`
notifiers:
  - name: n
    type: slack
    url: https://hooks.example/x
    digest: { every: monthly, at: 8h, timezone: Mars/Olympus, immediate_level: fatal }
apis: []
`))
	if err == nil {
		t.Fatal("configuration invalide acceptée")
	}
	for _, want := range []string{"digest.every", "digest.at", "digest.timezone", "digest.immediate_level"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("pas d'erreur pour %s : %v", want, err)
		}
	}
	c, err := Parse([]byte(`
notifiers:
  - { name: n, type: slack, url: "https://hooks.example/x", digest: { every: weekly } }
apis:
  - { id: a, name: A, source: { url: "https://p.example/openapi.json" } }
`))
	if err != nil {
		t.Fatal(err)
	}
	if d := c.Notifiers[0].Digest; d.At != "08:00" || d.Day != "monday" || d.Timezone != "Europe/Paris" {
		t.Errorf("valeurs par défaut : %+v", d)
	}
}
