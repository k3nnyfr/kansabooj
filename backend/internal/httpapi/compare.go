package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/diff"
	"git.k3nny.fr/kansabooj/internal/normalize"
	"git.k3nny.fr/kansabooj/internal/store"
)

func (s *Server) snapshots(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSnapshots(r.Context(), r.PathValue("id"), intParam(r, "limit", 200, 1, 1000))
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type compareSide struct {
	ID       int64 `json:"id"`
	Semantic bool  `json:"semantic"`
}

// compare diffs two arbitrary versions of an API: semantic changes (oasdiff, with the
// API's current re-exposed scope) and a line diff of the normalized documents.
func (s *Server) compare(w http.ResponseWriter, r *http.Request) {
	apiID := r.PathValue("id")
	a, err := s.store.GetAPI(r.Context(), apiID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "API inconnue")
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	var docs [2][]byte
	var sides [2]compareSide
	for i, param := range []string{"from", "to"} {
		id, err := strconv.ParseInt(r.URL.Query().Get(param), 10, 64)
		if err != nil || id <= 0 {
			writeErr(w, http.StatusBadRequest, "paramètres from et to attendus : identifiants de versions (GET /api/apis/{id}/snapshots)")
			return
		}
		owner, body, media, err := s.store.SnapshotSpec(r.Context(), id, true)
		if errors.Is(err, store.ErrNotFound) || (err == nil && owner != apiID) {
			writeErr(w, http.StatusNotFound, "version "+strconv.FormatInt(id, 10)+" inconnue pour cette API")
			return
		} else if err != nil {
			s.internal(w, err)
			return
		}
		docs[i], sides[i] = body, compareSide{ID: id, Semantic: !strings.HasPrefix(media, "text/plain")}
		if sides[i].Semantic { // versions stored by an older normalization: same form on both sides
			if docs[i], _, err = normalize.Upgrade(body); err != nil {
				s.internal(w, err)
				return
			}
		}
	}

	var summary *diff.Summary
	switch {
	case sides[0].ID == sides[1].ID:
		summary = &diff.Summary{Items: []diff.Item{}}
	case sides[0].Semantic && sides[1].Semantic:
		prev, err1 := normalize.Load(docs[0])
		cur, err2 := normalize.Load(docs[1])
		if err := errors.Join(err1, err2); err != nil {
			s.internal(w, err)
			return
		}
		var cfg config.API
		_ = json.Unmarshal([]byte(a.ConfigJSON), &cfg) // exposed_operations: current scope of the API
		if summary, err = diff.Compare(prev, cur, cfg.Gateway.ExposedOperations); err != nil {
			s.internal(w, err)
			return
		}
	case !sides[0].Semantic && !sides[1].Semantic:
		summary = diff.CompareText(docs[0], docs[1])
	default:
		summary = diff.ModeChanged(mode(sides[0].Semantic), mode(sides[1].Semantic), "")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from":      sides[0],
		"to":        sides[1],
		"summary":   summary,
		"text_diff": diff.LineDiff(lines(docs[0]), lines(docs[1]), 3),
	})
}

func mode(semantic bool) string {
	if semantic {
		return "spec"
	}
	return "html"
}

func lines(b []byte) []string {
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}
