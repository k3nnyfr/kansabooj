package notify

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/diff"
	"git.k3nny.fr/kansabooj/internal/i18n"
	"git.k3nny.fr/kansabooj/internal/store"
)

const (
	KindDigest = "digest"
	// content type of a queued event (neutral Event JSON, rendered when the digest is built)
	queuedContentType = "application/vnd.kansabooj.event+json"
	maxDigestLines    = 25
)

// DigestEntry summarises one change in a digest.
type DigestEntry struct {
	APIID       string    `json:"api_id"`
	APIName     string    `json:"api_name"`
	ChangeID    int64     `json:"change_id"`
	OccurredAt  time.Time `json:"occurred_at"`
	VersionFrom string    `json:"version_from,omitempty"`
	VersionTo   string    `json:"version_to,omitempty"`
	MaxLevel    string    `json:"max_level"`
	Errors      int       `json:"errors"`
	Warns       int       `json:"warns"`
	Infos       int       `json:"infos"`
	Link        string    `json:"link,omitempty"`
}

// digested reports whether a change event for n waits for the next digest instead of
// being sent now (immediate_level lets breaking changes through).
func digested(n *config.Notifier, e *Event) bool {
	if n.Digest == nil || e.Kind != KindChange {
		return false
	}
	return n.Digest.ImmediateLevel == "" || !diff.LevelAtLeast(e.MaxLevel, n.Digest.ImmediateLevel)
}

func (d *Dispatcher) queue(ctx context.Context, n *config.Notifier, e *Event) {
	body, err := json.Marshal(e)
	if err != nil {
		d.log.Error("mise en file pour récapitulatif", "notifier", n.Name, "err", err)
		return
	}
	rec := &store.Notification{APIID: e.APIID, Notifier: n.Name, Kind: e.Kind, Status: "queued", Payload: body, ContentType: queuedContentType, At: e.OccurredAt}
	if e.ChangeID != 0 {
		rec.ChangeID = &e.ChangeID
	}
	if _, err := d.store.CreateNotification(ctx, rec); err != nil {
		d.log.Error("mise en file pour récapitulatif", "notifier", n.Name, "err", err)
	}
}

// DigestLoop builds due digests every `every` until ctx is done (leader only).
func (d *Dispatcher) DigestLoop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		d.RunDigests(ctx, d.now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunDigests sends, for each digest notifier whose schedule is due, one message
// summarising the changes queued since its previous digest. Nothing queued: nothing
// sent, the run is only recorded.
func (d *Dispatcher) RunDigests(ctx context.Context, now time.Time) {
	cfg := d.cfg()
	for i := range cfg.Notifiers {
		n := &cfg.Notifiers[i]
		if n.Digest == nil {
			continue
		}
		due := n.Digest.LastDue(now)
		last, err := d.store.DigestLastRun(ctx, n.Name)
		if err != nil {
			d.log.Error("récapitulatif : lecture du dernier envoi", "notifier", n.Name, "err", err)
			continue
		}
		if last != nil && !last.Before(due) {
			continue
		}
		queued, err := d.store.QueuedNotifications(ctx, n.Name, due)
		if err != nil {
			d.log.Error("récapitulatif : lecture de la file", "notifier", n.Name, "err", err)
			continue
		}
		var msg *store.Notification
		covered := make([]int64, 0, len(queued))
		var entries []DigestEntry
		for _, q := range queued {
			covered = append(covered, q.ID)
			var e Event
			if err := json.Unmarshal(q.Payload, &e); err != nil {
				d.log.Warn("récapitulatif : événement illisible ignoré", "notifier", n.Name, "id", q.ID, "err", err)
				continue
			}
			entries = append(entries, entryOf(&e))
		}
		if len(entries) > 0 {
			loc, _ := time.LoadLocation(n.Digest.Timezone)
			since := entries[0].OccurredAt
			if last != nil {
				since = *last
			}
			if loc != nil {
				since = since.In(loc)
			}
			ev := &Event{Kind: KindDigest, OccurredAt: now.UTC(), Link: link(cfg.Server.BaseURL, "/"), Digest: entries, Since: &since}
			body, err := payload(n.Type, n.Language, ev)
			if err != nil {
				d.log.Error("rendu du récapitulatif", "notifier", n.Name, "err", err)
				continue
			}
			// api_id is a foreign key: the digest row is attached to the first API it covers
			msg = &store.Notification{APIID: queued[0].APIID, Payload: body, ContentType: "application/json"}
		}
		if err := d.store.CompleteDigest(ctx, n.Name, due, covered, msg); err != nil {
			d.log.Error("récapitulatif : enregistrement", "notifier", n.Name, "err", err)
			continue
		}
		if msg != nil {
			d.log.Info("récapitulatif", "notifier", n.Name, "changes", len(entries), "due", due)
			d.attempt(ctx, n, msg)
		}
	}
}

func entryOf(e *Event) DigestEntry {
	de := DigestEntry{APIID: e.APIID, APIName: e.APIName, ChangeID: e.ChangeID, OccurredAt: e.OccurredAt,
		VersionFrom: e.VersionFrom, VersionTo: e.VersionTo, MaxLevel: e.MaxLevel, Link: e.Link}
	for _, it := range e.Items {
		switch it.Level {
		case diff.LevelError:
			de.Errors++
		case diff.LevelWarn:
			de.Warns++
		default:
			de.Infos++
		}
	}
	return de
}

func renderDigest(e *Event, lang string) message {
	apis := map[string]bool{}
	for _, de := range e.Digest {
		apis[de.APIID] = true
	}
	m := message{Link: e.Link, Button: i18n.T(lang, "button.open")}
	m.Title = i18n.T(lang, "digest.title", len(e.Digest), len(apis))
	if e.Since != nil {
		m.Subtitle = i18n.T(lang, "digest.since", e.Since.Format("2006-01-02 15:04"))
	}
	for i, de := range e.Digest {
		if i == maxDigestLines {
			m.Lines = append(m.Lines, i18n.T(lang, "digest.more", len(e.Digest)-maxDigestLines))
			break
		}
		var parts []string
		for j, c := range []int{de.Errors, de.Warns, de.Infos} {
			if c > 0 {
				parts = append(parts, i18n.T(lang, []string{"change.breaking", "change.warn", "change.info"}[j], c))
			}
		}
		version := i18n.T(lang, "change.noversion", na(de.VersionTo))
		if de.VersionFrom != de.VersionTo {
			version = i18n.T(lang, "change.version", na(de.VersionFrom), na(de.VersionTo))
		}
		level := de.MaxLevel
		if level == "" {
			level = diff.LevelInfo
		}
		m.Lines = append(m.Lines, i18n.T(lang, "digest.line", i18n.T(lang, "level."+level), de.APIName, version, strings.Join(parts, ", ")))
	}
	return m
}
