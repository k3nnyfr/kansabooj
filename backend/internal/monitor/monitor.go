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
	"git.k3nny.fr/kansabooj/internal/discover"
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
	PrevStatus        string   // API status before this check (pending | ok | error | auth_error | stale)
	ResolvedURL       string   // spec URL actually used (discovered behind a page, or the source URL)
	Discovery         string   // how the spec was found, when discovered
	Alternatives      []string // other specs declared by the page
	Duration          time.Duration
}

type actorKey struct{}

// WithActor marks ctx as carrying a check asked by name (SSO user, token:<name>, anonymous, cli):
// the check log records it, for the journal.
func WithActor(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, actorKey{}, name)
}

func actorOf(ctx context.Context) string {
	name, _ := ctx.Value(actorKey{}).(string)
	return name
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
	rec := store.CheckRecord{APIID: apiID, StartedAt: start, Trigger: trigger, Actor: actorOf(ctx)}

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

	client, err := m.client(api)
	if err != nil {
		return fail("config", err, 0)
	}
	doc, err := m.retrieve(ctx, api, client, state, dryRun)
	if err != nil {
		var fe *fetch.Error
		if errors.As(err, &fe) {
			return fail(string(fe.Kind), fe, fe.Status)
		}
		return fail("invalid_spec", err, 0) // nothing usable found behind the page
	}
	resp := doc.resp
	out.FetchedURL, out.ResolvedURL, out.Discovery, out.Alternatives = resp.URL, doc.url, doc.how, doc.alternatives
	rec.HTTPStatus, rec.ETag, rec.LastMod = resp.Status, resp.ETag, resp.LastModified
	if !dryRun && m.store != nil {
		if err := m.store.SetResolvedURL(ctx, apiID, doc.url); err != nil {
			return nil, err
		}
	}

	if resp.NotModified {
		out.Status, out.NotModified, out.Duration = StatusUnchanged, true, time.Since(start)
		if dryRun || m.store == nil {
			return out, nil
		}
		rec.Status, rec.Duration = StatusUnchanged, out.Duration
		return out, m.store.RecordUnchanged(ctx, rec)
	}

	var spec *normalize.Result
	if doc.html {
		spec, err = normalize.HTMLText(resp.Body, api.Source.TextIgnore)
	} else {
		spec, err = normalize.NormalizeWith(resp.Body, normalize.Options{
			Ignore: api.Ignore, Location: resp.URL, Ctx: ctx,
			Fetch: func(ctx context.Context, u string) ([]byte, error) { // external $refs: same client and policy
				r, err := client.Get(ctx, fetch.Request{URL: u})
				if err != nil {
					return nil, err
				}
				return r.Body, nil
			},
		})
	}
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
		prevHTML, curHTML := state.Current.Format == normalize.FormatHTML, spec.Format == normalize.FormatHTML
		switch {
		case prevHTML && curHTML:
			out.Summary = diff.CompareText(prevCanonical, spec.Canonical)
		case prevHTML != curHTML: // e.g. a spec was published behind a page previously captured as text
			out.Summary = diff.ModeChanged(state.Current.Format, spec.Format, spec.Version)
		default:
			prev, err := normalize.Load(prevCanonical)
			if err != nil {
				return nil, fmt.Errorf("relecture de la version précédente : %w", err)
			}
			if out.Summary, err = diff.Compare(prev, spec.Doc, api.Gateway.ExposedOperations); err != nil {
				return fail("diff", err, resp.Status)
			}
		}
		prevID = &state.Current.ID
	} else if spec.Format == normalize.FormatHTML {
		out.Summary = diff.FirstCaptureText(bytes.Count(spec.Canonical, []byte("\n")))
	} else {
		out.Summary = diff.FirstCapture(spec.Doc, spec.OperationCount)
	}
	out.Status, out.Duration = StatusChanged, time.Since(start)
	if dryRun {
		return out, nil
	}

	rec.Status, rec.Duration = StatusChanged, out.Duration
	out.ChangeID, err = m.store.SaveNewVersion(ctx, store.NewVersion{
		Check:           rec,
		Raw:             resp.Body,
		RawMedia:        mediaType(resp.ContentType, resp.Body),
		Normalized:      spec.Canonical,
		NormalizedMedia: normalizedMedia(spec.Format),
		Snapshot: store.Snapshot{Format: spec.Format, Version: spec.Version, Title: spec.Title,
			OperationCount: spec.OperationCount},
		PrevSnapID: prevID,
		Summary:    out.Summary,
		Semantic:   spec.Format != normalize.FormatHTML,
	})
	return out, err
}

// document is what a check retrieved for an API.
type document struct {
	resp         *fetch.Response
	url          string // URL of the document (spec, or page in html mode)
	how          string
	alternatives []string
	html         bool // degraded mode: compare the page text
}

// retrieve fetches the document according to the source type:
//   - openapi: the URL is the spec;
//   - html: the page itself, compared as text (degraded mode);
//   - swagger-ui / redoc / auto: the page is a documentation UI; the spec behind it is
//     discovered once, then fetched directly (with conditional requests) as long as it
//     keeps working, and rediscovered when it disappears.
func (m *Monitor) retrieve(ctx context.Context, api *config.API, c *fetch.Client, state *store.APIState, dryRun bool) (*document, error) {
	cond := func(u string) fetch.Request {
		r := fetch.Request{URL: u}
		if !dryRun && state != nil && state.Current != nil && state.ResolvedURL == u {
			r.ETag, r.LastModified = state.ETag, state.LastModified
		}
		return r
	}
	src := api.Source.URL
	switch api.Source.Type {
	case config.SourceOpenAPI:
		resp, err := c.Get(ctx, cond(src))
		if err != nil {
			return nil, err
		}
		return &document{resp: resp, url: src}, nil
	case config.SourceHTML:
		resp, err := c.Get(ctx, cond(src))
		if err != nil {
			return nil, err
		}
		return &document{resp: resp, url: src, html: true, how: "mode dégradé (texte de la page)"}, nil
	}

	prevHTML := state != nil && state.Current != nil && state.Current.Format == normalize.FormatHTML
	if state != nil && state.ResolvedURL != "" && state.ResolvedURL != src && !prevHTML {
		resp, err := c.Get(ctx, cond(state.ResolvedURL))
		if err == nil && (resp.NotModified || isSpec(resp.Body)) {
			return &document{resp: resp, url: state.ResolvedURL, how: "spec découverte précédemment"}, nil
		}
		// gone, moved or no longer a spec: discover again from the page
	}

	page, err := c.Get(ctx, fetch.Request{URL: src})
	if err != nil {
		return nil, err
	}
	if !isHTML(page) { // the source URL is the spec itself
		return &document{resp: page, url: src}, nil
	}
	res, err := discover.Discover(ctx, c, src, page.Body, discover.Options{SpecName: api.Source.SpecName, SkipHeuristics: prevHTML})
	if err != nil {
		if api.Source.Type == config.SourceAuto && errors.Is(err, discover.ErrNotFound) {
			return &document{resp: page, url: src, html: true, how: "aucune spec trouvée : mode dégradé (texte de la page)"}, nil
		}
		return nil, fmt.Errorf("%w — renseigner l'URL du fichier de spec (type openapi) ou utiliser le type html", err)
	}
	return &document{resp: res.Response, url: res.SpecURL, how: res.Method, alternatives: res.Alternatives}, nil
}

func isHTML(r *fetch.Response) bool {
	if strings.Contains(strings.ToLower(r.ContentType), "html") {
		return true
	}
	t := bytes.TrimSpace(r.Body)
	return len(t) > 0 && t[0] == '<'
}

func isSpec(b []byte) bool { return normalize.IsSpec(b) }

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

func normalizedMedia(format string) string {
	if format == normalize.FormatHTML {
		return "text/plain"
	}
	return "application/json"
}

func mediaType(contentType string, body []byte) string {
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "html"):
		return "text/html"
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
