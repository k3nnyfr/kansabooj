package fetch

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"git.k3nny.fr/kansabooj/internal/config"
)

// originAuth adds credentials only to requests whose origin equals the configured
// source origin. Redirects, discovered specs and external $refs on another host
// are therefore fetched anonymously. (net/http forwards custom headers such as
// X-API-Key on cross-domain redirects, so setting them on the request is unsafe.)
type originAuth struct {
	base   http.RoundTripper
	origin string
	apply  func(*http.Request) error
}

func (t *originAuth) RoundTrip(r *http.Request) (*http.Response, error) {
	if config.OriginOf(r.URL) != t.origin {
		return t.base.RoundTrip(r)
	}
	r = r.Clone(r.Context())
	if err := t.apply(r); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(r)
}

// authenticator returns the function that decorates a request, and a hint naming
// what to check when the server refuses the credentials.
func authenticator(a *config.Auth, tokenClient *http.Client) (func(*http.Request) error, string, error) {
	switch a.Type {
	case "header":
		return func(r *http.Request) error {
			r.Header.Set(a.Name, a.Value)
			return nil
		}, fmt.Sprintf("la valeur de l'en-tête %s", a.Name), nil
	case "query":
		return func(r *http.Request) error {
			q := r.URL.Query()
			q.Set(a.Name, a.Value)
			u := *r.URL
			u.RawQuery = q.Encode()
			r.URL = &u
			return nil
		}, fmt.Sprintf("la valeur du paramètre %s", a.Name), nil
	case "basic":
		return func(r *http.Request) error {
			r.SetBasicAuth(a.Username, a.Password)
			return nil
		}, "l'identifiant / mot de passe", nil
	case "bearer":
		return func(r *http.Request) error {
			r.Header.Set("Authorization", "Bearer "+a.Token)
			return nil
		}, "le token bearer", nil
	case "oauth2_client_credentials":
		cc := clientcredentials.Config{
			ClientID:       a.ClientID,
			ClientSecret:   a.ClientSecret,
			TokenURL:       a.TokenURL,
			Scopes:         a.Scopes,
			EndpointParams: url.Values{},
		}
		for k, v := range a.Params {
			cc.EndpointParams.Set(k, v)
		}
		// The token endpoint goes through the same proxy / CA, without the API credentials.
		ctx := context.WithValue(context.Background(), oauth2.HTTPClient, tokenClient)
		ts := cc.TokenSource(ctx) // caches the token until expiry
		return func(r *http.Request) error {
			tok, err := ts.Token()
			if err != nil {
				return &Error{Kind: KindAuth, Msg: fmt.Sprintf("obtention du token OAuth2 sur %s impossible : %v", a.TokenURL, err), Err: err}
			}
			tok.SetAuthHeader(r)
			return nil
		}, "client_id / client_secret OAuth2 et les scopes", nil
	}
	return nil, "", fmt.Errorf("type d'authentification %q non supporté", a.Type)
}
