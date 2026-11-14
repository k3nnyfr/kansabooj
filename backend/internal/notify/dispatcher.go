package notify

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/diff"
	"git.k3nny.fr/kansabooj/internal/fetch"
	"git.k3nny.fr/kansabooj/internal/monitor"
	"git.k3nny.fr/kansabooj/internal/store"
)

// backoff between delivery attempts; after the last one the notification is abandoned.
var backoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour}

type Dispatcher struct {
	store  *store.Store
	cfg    func() *config.Config
	client *http.Client
	log    *slog.Logger
	now    func() time.Time
}

func NewDispatcher(st *store.Store, cfg func() *config.Config, log *slog.Logger) (*Dispatcher, error) {
	tr, err := fetch.OutboundTransport(cfg().HTTP)
	if err != nil {
		return nil, err
	}
	return &Dispatcher{store: st, cfg: cfg, log: log, now: time.Now,
		client: &http.Client{Transport: tr, Timeout: 15 * time.Second}}, nil
}

// HandleOutcome decides which notifications a check outcome triggers, stores and sends them.
// First captures are never notified (onboarding a new API must not spam channels).
func (d *Dispatcher) HandleOutcome(ctx context.Context, api *config.API, o *monitor.Outcome) {
	cfg := d.cfg()
	base := Event{APIID: api.ID, APIName: api.Name, Owner: api.Owner, OccurredAt: d.now().UTC()}
	var events []Event
	switch {
	case o.Status == monitor.StatusChanged && o.Summary != nil && o.ChangeID != 0 && !isFirstCapture(o.Summary):
		e := base
		e.Kind, e.ChangeID = KindChange, o.ChangeID
		e.VersionFrom, e.VersionTo = o.Summary.VersionFrom, o.Summary.VersionTo
		e.Link = link(cfg.Server.BaseURL, fmt.Sprintf("/apis/%s?change=%d", api.ID, o.ChangeID))
		e.Items = o.Summary.Items
		events = append(events, e)
	case o.Status == monitor.StatusError && o.ConsecutiveErrors == cfg.Defaults.ErrorThreshold:
		e := base
		e.Kind, e.Error, e.ConsecutiveErrors = KindStale, fmt.Sprint(o.Err), o.ConsecutiveErrors
		e.Link = link(cfg.Server.BaseURL, "/apis/"+api.ID)
		events = append(events, e)
	case o.Status != monitor.StatusError && o.PrevStatus == "stale":
		e := base
		e.Kind = KindRecovered
		e.Link = link(cfg.Server.BaseURL, "/apis/"+api.ID)
		events = append(events, e)
	}

	for _, ev := range events {
		for _, n := range recipients(cfg, api) {
			e, ok := selectFor(ev, n)
			if !ok {
				continue
			}
			d.enqueue(ctx, n, &e)
		}
	}
}

// selectFor applies min_level and only_in_scope. Stale/recovered always go through.
func selectFor(e Event, n *config.Notifier) (Event, bool) {
	if e.Kind != KindChange {
		return e, true
	}
	var items []diff.Item
	omitted := 0
	for _, it := range e.Items {
		if *n.OnlyInScope && !it.InScope {
			omitted++
			continue
		}
		items = append(items, it)
	}
	e.Items, e.OmittedOutOfScope, e.counts, e.MaxLevel = items, omitted, [3]int{}, ""
	for _, it := range items {
		switch it.Level {
		case diff.LevelError:
			e.counts[0]++
		case diff.LevelWarn:
			e.counts[1]++
		default:
			e.counts[2]++
		}
		if e.MaxLevel == "" || diff.LevelAtLeast(it.Level, e.MaxLevel) {
			e.MaxLevel = it.Level
		}
	}
	if e.MaxLevel == "" || !diff.LevelAtLeast(e.MaxLevel, n.MinLevel) {
		return e, false
	}
	return e, true
}

func (d *Dispatcher) enqueue(ctx context.Context, n *config.Notifier, e *Event) {
	body, err := payload(n.Type, n.Language, e)
	if err != nil {
		d.log.Error("rendu de notification", "notifier", n.Name, "err", err)
		return
	}
	rec := &store.Notification{APIID: e.APIID, Notifier: n.Name, Kind: e.Kind, Payload: body, ContentType: "application/json"}
	if e.ChangeID != 0 {
		rec.ChangeID = &e.ChangeID
	}
	created, err := d.store.CreateNotification(ctx, rec)
	if err != nil {
		d.log.Error("enregistrement de notification", "notifier", n.Name, "err", err)
		return
	}
	if created {
		d.attempt(ctx, n, rec)
	}
}

// RetryLoop resends due notifications until ctx is done.
func (d *Dispatcher) RetryLoop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			due, err := d.store.DueNotifications(ctx, d.now())
			if err != nil {
				d.log.Error("lecture des notifications en attente", "err", err)
				continue
			}
			for i := range due {
				n := notifierByName(d.cfg(), due[i].Notifier)
				if n == nil {
					d.store.MarkNotification(ctx, due[i].ID, "notifier supprimé de la configuration", nil)
					continue
				}
				d.attempt(ctx, n, &due[i])
			}
		}
	}
}

func (d *Dispatcher) attempt(ctx context.Context, n *config.Notifier, rec *store.Notification) {
	err := d.send(ctx, n, rec.Payload)
	if err == nil {
		d.log.Info("notification envoyée", "notifier", n.Name, "api", rec.APIID, "kind", rec.Kind)
		d.store.MarkNotification(ctx, rec.ID, "", nil)
		return
	}
	var next *time.Time
	if rec.Attempts < len(backoff) {
		t := d.now().Add(backoff[rec.Attempts])
		next = &t
	}
	d.log.Warn("échec d'envoi de notification", "notifier", n.Name, "api", rec.APIID, "attempt", rec.Attempts+1, "err", err, "abandon", next == nil)
	d.store.MarkNotification(ctx, rec.ID, err.Error(), next)
}

func (d *Dispatcher) send(ctx context.Context, n *config.Notifier, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", d.cfg().HTTP.UserAgent)
	if n.Type == "generic" && n.Secret != "" {
		req.Header.Set("X-Kansabooj-Signature", Sign(n.Secret, body))
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d : %s", resp.StatusCode, bytes.TrimSpace(msg))
	}
	return nil
}

// recipients are the notifiers listed by the API (or inherited from its group /
// defaults) plus those subscribed to its group or tags.
func recipients(cfg *config.Config, api *config.API) []*config.Notifier {
	var out []*config.Notifier
	for i := range cfg.Notifiers {
		n := &cfg.Notifiers[i]
		if slices.Contains(api.Notify, n.Name) || n.Subscribed(api) {
			out = append(out, n)
		}
	}
	return out
}

func notifierByName(cfg *config.Config, name string) *config.Notifier {
	for i := range cfg.Notifiers {
		if cfg.Notifiers[i].Name == name {
			return &cfg.Notifiers[i]
		}
	}
	return nil
}

func isFirstCapture(s *diff.Summary) bool {
	return len(s.Items) == 1 && s.Items[0].RuleID == diff.RuleFirstCapture
}

func link(base, path string) string {
	if base == "" {
		return ""
	}
	return base + path
}
