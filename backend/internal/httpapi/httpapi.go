// Package httpapi exposes the REST API consumed by the UI, and serves the UI itself.
package httpapi

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/monitor"
	"git.k3nny.fr/kansabooj/internal/scheduler"
	"git.k3nny.fr/kansabooj/internal/store"
)

// Version is set at build time (-ldflags "-X .../httpapi.Version=...").
var Version = "0.2.0-dev"

//go:embed all:web
var web embed.FS

// Runtime is what the API needs from the scheduler.
type Runtime interface {
	Config() *config.Config
	ConfigPath() string
	TriggerCheck(ctx context.Context, apiID string) (*monitor.Outcome, error)
	Reload(ctx context.Context) error
	Status() scheduler.Status
}

type Server struct {
	rt    Runtime
	store *store.Store
	log   *slog.Logger
}

func New(rt Runtime, st *store.Store, log *slog.Logger) *Server {
	return &Server{rt: rt, store: st, log: log}
}

// route is an API endpoint; scope "" means public (probes, API description).
type route struct {
	method, path, scope string
	handler             http.HandlerFunc
}

func (s *Server) routes() []route {
	return []route{
		{"GET", "/healthz", "", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }}, // liveness
		{"GET", "/readyz", "", s.ready}, // readiness
		{"GET", "/api/openapi.yaml", "", s.openapi},
		{"GET", "/api/meta", "read", s.meta},
		{"GET", "/api/status", "read", s.status},
		{"GET", "/api/apis", "read", s.listAPIs},
		{"GET", "/api/apis/{id}", "read", s.getAPI},
		{"GET", "/api/apis/{id}/timeline", "read", s.timeline},
		{"POST", "/api/apis/{id}/check", "check", s.check},
		{"POST", "/api/apis/{id}/ack", "ack", s.ackAll},
		{"GET", "/api/changes/{id}", "read", s.getChange},
		{"POST", "/api/changes/{id}/ack", "ack", s.ack},
		{"GET", "/api/snapshots/{id}/spec", "read", s.spec},
		{"POST", "/api/config/validate", "validate", s.validateConfig},
		{"POST", "/api/admin/reload", "admin", s.reload},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, rt := range s.routes() {
		h := rt.handler
		if rt.scope != "" {
			h = s.authorize(rt.scope, h)
		}
		mux.HandleFunc(rt.method+" "+rt.path, h)
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { writeErr(w, http.StatusNotFound, "route inconnue") })
	mux.Handle("/", spa())
	return s.logged(mux)
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Write([]byte("ok"))
}

func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	cfg := s.rt.Config()
	groups := cfg.Groups
	if groups == nil {
		groups = []config.Group{}
	}
	tags := []string{}
	for _, a := range cfg.APIs {
		for _, t := range a.Tags {
			if !slices.Contains(tags, t) {
				tags = append(tags, t)
			}
		}
	}
	slices.Sort(tags)
	writeJSON(w, http.StatusOK, map[string]any{
		"version":   Version,
		"language":  cfg.Server.Language,
		"languages": config.Languages,
		"user":      s.user(r),
		"groups":    groups, // secrets and technical defaults are not serialized (json:"-")
		"tags":      tags,
	})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	apis, err := s.store.ListAPIs(r.Context(), false)
	if err != nil {
		s.internal(w, err)
		return
	}
	failing := []store.APIRow{}
	for _, a := range apis {
		if a.Status != "ok" && a.Status != "pending" {
			failing = append(failing, a)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"scheduler": s.rt.Status(), "failing": failing, "total": len(apis)})
}

func (s *Server) listAPIs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	apis, err := s.store.ListAPIs(r.Context(), q.Get("all") == "1")
	if err != nil {
		s.internal(w, err)
		return
	}
	group, tag := q.Get("group"), q.Get("tag")
	out := apis[:0]
	for _, a := range apis {
		if (group == "" || a.Group == group) && (tag == "" || slices.Contains(a.Tags, tag)) {
			out = append(out, a)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getAPI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, err := s.store.GetAPI(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "API inconnue")
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	checks, err := s.store.RecentChecks(r.Context(), id, 20)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"api":    a,
		"config": json.RawMessage(a.ConfigJSON), // secrets are never part of config_json
		"checks": checks,
	})
}

func (s *Server) timeline(w http.ResponseWriter, r *http.Request) {
	limit := intParam(r, "limit", 30, 1, 200)
	before := int64(intParam(r, "before", 0, 0, 1<<62))
	tl, err := s.store.TimelinePage(r.Context(), r.PathValue("id"), limit, before)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tl)
}

func (s *Server) getChange(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	d, err := s.store.GetChange(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "évolution inconnue")
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) ack(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.Acknowledge(r.Context(), id, s.user(r)); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "évolution inconnue")
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ackAll(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.AcknowledgeAll(r.Context(), r.PathValue("id"), s.user(r))
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"acknowledged": n})
}

func (s *Server) check(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	o, err := s.rt.TriggerCheck(ctx, r.PathValue("id"))
	switch {
	case errors.Is(err, scheduler.ErrUnknownAPI):
		writeErr(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, scheduler.ErrBusy):
		writeErr(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		s.internal(w, err)
		return
	}
	res := map[string]any{"status": o.Status, "not_modified": o.NotModified, "duration_ms": o.Duration.Milliseconds()}
	if o.Err != nil {
		res["error"], res["error_kind"] = o.Err.Error(), o.ErrorKind
	}
	if o.ChangeID != 0 {
		res["change_id"] = o.ChangeID
	}
	if o.Summary != nil {
		res["summary"] = map[string]int{"errors": o.Summary.Errors, "warns": o.Summary.Warns, "infos": o.Summary.Infos, "in_scope": o.Summary.InScope}
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) spec(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	normalized := r.URL.Query().Get("form") != "raw"
	apiID, body, media, err := s.store.SnapshotSpec(r.Context(), id, normalized)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "version inconnue")
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	ext := "yaml"
	if strings.Contains(media, "json") {
		ext = "json"
	}
	form := "raw"
	if normalized {
		form = "normalized"
	}
	w.Header().Set("Content-Type", media+"; charset=utf-8")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%d-%s.%s"`, apiID, id, form, ext))
	}
	w.Write(body)
}

func (s *Server) reload(w http.ResponseWriter, r *http.Request) {
	if err := s.rt.Reload(r.Context()); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"apis": len(s.rt.Config().APIs)})
}

func (s *Server) user(r *http.Request) string {
	if id, ok := r.Context().Value(identityKey{}).(*identity); ok {
		return id.name
	}
	return "anonymous"
}

func (s *Server) internal(w http.ResponseWriter, err error) {
	s.log.Error("erreur interne", "err", err)
	writeErr(w, http.StatusInternalServerError, "erreur interne")
}

func (s *Server) logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			s.log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "duration", time.Since(start).Round(time.Millisecond))
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// spa serves the built UI (web/dist) with index.html fallback for client-side
// routes, or a placeholder page when the UI has not been built.
func spa() http.Handler {
	dist, err := fs.Sub(web, "web/dist")
	if _, statErr := fs.Stat(web, "web/dist/index.html"); err != nil || statErr != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := web.ReadFile("web/placeholder.html")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(b)
		})
	}
	files := http.FileServerFS(dist)
	index, _ := fs.ReadFile(dist, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(dist, p); err == nil {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "identifiant invalide")
		return 0, false
	}
	return id, true
}

func intParam(r *http.Request, name string, def, min, max int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return def
	}
	return clamp(v, min, max)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
