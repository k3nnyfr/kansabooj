package httpapi

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/token"
)

type identityKey struct{}

type identity struct {
	name   string   // SSO user, "token:<name>" or "anonymous"
	scopes []string // nil = every scope
}

func (id *identity) can(scope string) bool {
	return id.scopes == nil || slices.Contains(id.scopes, scope)
}

// ssoScopes are the rights of a user authenticated by the SSO reverse proxy.
var ssoScopes = []string{"read", "check", "ack", "validate"}

// authorize resolves the caller and enforces the scope of the route:
//   - "Authorization: Bearer kbj_…" → configured token, with its scopes (invalid token → 401);
//   - SSO header (server.user_header) → read/check/ack/validate, plus admin for server.admins
//     (or for everyone when require_identity is off);
//   - nothing → anonymous with every right, unless server.require_identity is set (→ 401).
func (s *Server) authorize(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := s.rt.Config()
		id, status, msg := identify(r, &cfg.Server)
		if id == nil {
			if status == http.StatusUnauthorized {
				w.Header().Set("WWW-Authenticate", `Bearer realm="kansabooj"`)
			}
			writeErr(w, status, msg)
			return
		}
		if !id.can(scope) {
			writeErr(w, http.StatusForbidden, "droit « "+scope+" » requis pour "+id.name)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, id)))
	}
}

func identify(r *http.Request, sv *config.Server) (*identity, int, string) {
	if auth := r.Header.Get("Authorization"); auth != "" {
		tok, ok := strings.CutPrefix(auth, "Bearer ")
		if !ok {
			return nil, http.StatusUnauthorized, "en-tête Authorization : « Bearer <jeton> » attendu"
		}
		tok = strings.TrimSpace(tok)
		for _, t := range sv.Tokens {
			if token.Matches(tok, t.Hash) {
				return &identity{name: "token:" + t.Name, scopes: t.Scopes}, 0, ""
			}
		}
		return nil, http.StatusUnauthorized, "jeton invalide"
	}
	if u := strings.TrimSpace(r.Header.Get(sv.UserHeader)); u != "" {
		if !sv.RequireIdentity || slices.Contains(sv.Admins, u) {
			return &identity{name: u}, 0, ""
		}
		return &identity{name: u, scopes: ssoScopes}, 0, ""
	}
	if sv.RequireIdentity {
		return nil, http.StatusUnauthorized, "identité requise (SSO ou jeton d'API)"
	}
	return &identity{name: "anonymous"}, 0, ""
}
