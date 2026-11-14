// Package monitor runs one check of an API: fetch → normalize → compare → persist.
// It is used by the CLI today and by the scheduler in v0.2.
package monitor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/diff"
	"git.k3nny.fr/kansabooj/internal/fetch"
	"git.k3nny.fr/kansabooj/internal/normalize"
	"git.k3nny.fr/kansabooj/internal/store"
)

const (
	StatusUnchanged = "unchanged"
	StatusChanged   = "changed"
	StatusError     = "error"
)

type Monitor struct {
	cfg   *config.Config
	store *store.Store // nil allowed in dry-run without database

	mu      sync.Mutex
	clients map[string]*fetch.Client
}

func New(cfg *config.Config, st *store.Store) *Monitor {
	return &Monitor{cfg: cfg, store: st, clients: map[string]*fetch.Client{}}
}

// Outcome describes what a check found.
type Outcome struct {
	APIID             string
	Status            string
	NotModified       bool
	FetchedURL        string
	Spec              *normalize.Result // nil on error or 304
	Summary           *diff.Summary     // set when Status == changed
	ChangeID          int64
	Err               error
	ErrorKind         string
	ConsecutiveErrors int
	PrevStatus        string // API status before this check (pending | ok | error | auth_error | stale)
	Duration          time.Duration
}

// Check runs one check. With dryRun nothing is written and conditional requests
// are disabled so the document is always downloaded and analyzed.
func (m *Monitor) Check(ctx context.Context, apiID, trigger string, dryRun bool) (*Outcome, error) {
	api, ok := m.cfg.API(apiID)
	if !ok {
		return nil, fmt.Errorf("API %q absente de la configuration", apiID)
	}
	start := time.Now().UTC()
	out := &Outcome{APIID: apiID}
	rec := store.CheckRecord{APIID: apiID, StartedAt: start, Trigger: trigger}

	var state *store.APIState
	if m.store != nil {
		var err error
		if state, err = m.store.State(ctx, apiID); err != nil {
			return nil, err
		}
		out.PrevStatus = state.Status
	}

	fail := func(kind string, err error, httpStatus int) (*Outcome, error) {
		out.Status, out.Err, out.ErrorKind, out.Duration = StatusError, err, kind, time.Since(start)
		if dryRun || m.store == nil {
			return out, nil
		}
		rec.Status, rec.ErrorKind, rec.Error, rec.HTTPStatus, rec.Duration = StatusError, kind, err.Error(), httpStatus, out.Duration
		n, serr := m.store.RecordError(ctx, rec, m.cfg.Defaults.ErrorThreshold)
		out.ConsecutiveErrors = n
		return out, serr
	}

	switch api.Source.Type {
	case config.SourceSwaggerUI, config.SourceRedoc, config.SourceHTML:
		return fail("unsupported", fmt.Errorf("source.type %q pas encore supporté (prévu en v0.3) : renseigner l'URL directe du fichier de spec avec type openapi", api.Source.Type), 0)
	}

	client, err := m.client(api)
	if err != nil {
		return fail("config", err, 0)
	}
	req := fetch.Request{URL: api.Source.URL}
	if !dryRun && state != nil && state.Current != nil {
		req.ETag, req.LastModified = state.ETag, state.LastModified
	}
	resp, err := client.Get(ctx, req)
	if err != nil {
		var fe *fetch.Error
		if errors.As(err, &fe) {
			return fail(string(fe.Kind), fe, fe.Status)
		}
		return fail(string(fetch.KindNetwork), err, 0)
	}
	out.FetchedURL = resp.URL
	rec.HTTPStatus, rec.ETag, rec.LastMod = resp.Status, resp.ETag, resp.LastModified

	if resp.NotModified {
		out.Status, out.NotModified, out.Duration = StatusUnchanged, true, time.Since(start)
		if dryRun || m.store == nil {
			return out, nil
		}
		rec.Status, rec.Duration = StatusUnchanged, out.Duration
		return out, m.store.RecordUnchanged(ctx, rec)
	}

	spec, err := normalize.Normalize(resp.Body, api.Ignore)
	if err != nil {
		return fail("invalid_spec", err, resp.Status)
	}
	out.Spec = spec

	if state != nil && state.Current != nil && state.Current.NormalizedSHA256 == spec.SHA256 {
		out.Status, out.Duration = StatusUnchanged, time.Since(start)
		if dryRun {
			return out, nil
		}
		rec.Status, rec.Duration = StatusUnchanged, out.Duration
		return out, m.store.RecordUnchanged(ctx, rec)
	}

	var prevID *int64
	if state != nil && state.Current != nil {
		prevCanonical, err := m.store.Blob(ctx, state.Current.NormalizedSHA256)
		if err != nil {
			return nil, err
		}
		prev, err := normalize.Load(prevCanonical)
		if err != nil {
			return nil, fmt.Errorf("relecture de la version précédente : %w", err)
		}
		if out.Summary, err = diff.Compare(prev, spec.Doc, api.Gateway.ExposedOperations); err != nil {
			return fail("diff", err, resp.Status)
		}
		prevID = &state.Current.ID
	} else {
		out.Summary = diff.FirstCapture(spec.Doc, spec.OperationCount)
	}
	out.Status, out.Duration = StatusChanged, time.Since(start)
	if dryRun {
		return out, nil
	}

	rec.Status, rec.Duration = StatusChanged, out.Duration
	out.ChangeID, err = m.store.SaveNewVersion(ctx, store.NewVersion{
		Check:      rec,
		Raw:        resp.Body,
		RawMedia:   mediaType(resp.ContentType, resp.Body),
		Normalized: spec.Canonical,
		Snapshot: store.Snapshot{Format: spec.Format, Version: spec.Version, Title: spec.Title,
			OperationCount: spec.OperationCount},
		PrevSnapID: prevID,
		Summary:    out.Summary,
		Semantic:   true,
	})
	return out, err
}

func (m *Monitor) client(api *config.API) (*fetch.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.clients[api.ID]; ok {
		return c, nil
	}
	c, err := fetch.New(m.cfg.HTTP, api)
	if err != nil {
		return nil, err
	}
	m.clients[api.ID] = c
	return c, nil
}

func mediaType(contentType string, body []byte) string {
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "json"):
		return "application/json"
	case strings.Contains(ct, "yaml"):
		return "application/yaml"
	}
	if b := bytes.TrimSpace(body); len(b) > 0 && b[0] == '{' {
		return "application/json"
	}
	return "application/yaml"
}
