package httpapi

import (
	_ "embed"
	"errors"
	"io"
	"net/http"
	"regexp"

	"git.k3nny.fr/kansabooj/internal/config"
)

var fragmentName = regexp.MustCompile(`^[A-Za-z0-9._-]+\.ya?ml$`)

// validateConfig checks a configuration document with the server context
// (environment variables, secret files, notifiers, groups, profiles) without
// applying it — typically called from the CI of the configuration repository.
//
//   - ?fragment=<file>.yml : the body replaces (or adds) this fragment in the live
//     configuration, and the whole result is validated;
//   - otherwise the body is a complete main file (its includes are not resolved).
//
// Nothing is fetched: validation never reaches the declared URLs.
func (s *Server) validateConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	if err != nil || len(body) == 0 {
		writeErr(w, http.StatusBadRequest, "corps YAML attendu")
		return
	}
	if len(body) > 1<<20 {
		writeErr(w, http.StatusRequestEntityTooLarge, "document > 1 Mo")
		return
	}
	var cfg *config.Config
	mode := "main"
	if name := r.URL.Query().Get("fragment"); name != "" {
		if !fragmentName.MatchString(name) {
			writeErr(w, http.StatusBadRequest, "fragment : nom de fichier .yml attendu (sans chemin)")
			return
		}
		mode = "fragment"
		cfg, err = config.LoadWith(s.rt.ConfigPath(), map[string][]byte{name: body})
	} else {
		cfg, err = config.Parse(body)
	}

	res := map[string]any{"valid": err == nil, "mode": mode, "problems": []string{}}
	var ve *config.ValidationError
	switch {
	case errors.As(err, &ve):
		res["problems"] = ve.Problems
	case err != nil:
		res["problems"] = []string{err.Error()}
	}
	if cfg != nil {
		res["apis"], res["groups"] = len(cfg.APIs), len(cfg.Groups)
	}
	writeJSON(w, http.StatusOK, res)
}

//go:embed openapi.yaml
var openapiSpec []byte

func (s *Server) openapi(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Write(openapiSpec)
}
