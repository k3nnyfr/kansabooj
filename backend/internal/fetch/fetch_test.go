package fetch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"git.k3nny.fr/kansabooj/internal/config"
)

func api(url string, auth *config.Auth) *config.API {
	d := config.Duration(5 * time.Second)
	return &config.API{ID: "t", Source: config.Source{URL: url}, Auth: auth, Timeout: &d}
}

var httpCfg = config.HTTP{UserAgent: "test", MaxBodyBytes: 1 << 20}

func TestAuthConfinedToOrigin(t *testing.T) {
	var otherGot string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherGot = r.Header.Get("X-Key") + r.URL.Query().Get("key")
		w.Write([]byte("{}"))
	}))
	defer other.Close()
	var srcGot string
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srcGot = r.Header.Get("X-Key")
		http.Redirect(w, r, other.URL+"/spec.json", http.StatusFound)
	}))
	defer src.Close()

	c, err := New(httpCfg, api(src.URL+"/docs", &config.Auth{Type: "header", Name: "X-Key", Value: "secret"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), Request{URL: src.URL + "/docs"}); err != nil {
		t.Fatal(err)
	}
	if srcGot != "secret" {
		t.Errorf("source origin did not receive credentials: %q", srcGot)
	}
	if otherGot != "" {
		t.Errorf("credentials leaked to another origin: %q", otherGot)
	}
}

func TestAuthErrorIsExplicit(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer s.Close()
	c, _ := New(httpCfg, api(s.URL, &config.Auth{Type: "bearer", Token: "x"}))
	_, err := c.Get(context.Background(), Request{URL: s.URL})
	if !IsAuth(err) {
		t.Fatalf("expected auth error, got %v", err)
	}
}

func TestOAuth2ClientCredentials(t *testing.T) {
	tokenCalls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		r.ParseForm()
		if r.Form.Get("audience") != "docs" {
			t.Errorf("missing endpoint param")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
	})
	mux.HandleFunc("/spec", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write([]byte("{}"))
	})
	s := httptest.NewServer(mux)
	defer s.Close()

	c, err := New(httpCfg, api(s.URL+"/spec", &config.Auth{Type: "oauth2_client_credentials",
		TokenURL: s.URL + "/token", ClientID: "id", ClientSecret: "sec", Params: map[string]string{"audience": "docs"}}))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := c.Get(context.Background(), Request{URL: s.URL + "/spec"}); err != nil {
			t.Fatal(err)
		}
	}
	if tokenCalls != 1 {
		t.Errorf("token should be cached, got %d token calls", tokenCalls)
	}
}

func TestConditionalAndLimits(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Write(make([]byte, 2048))
	}))
	defer s.Close()

	c, _ := New(config.HTTP{UserAgent: "t", MaxBodyBytes: 1024}, api(s.URL, nil))
	if _, err := c.Get(context.Background(), Request{URL: s.URL}); err == nil {
		t.Error("expected too_large error")
	}
	resp, err := c.Get(context.Background(), Request{URL: s.URL, ETag: `"v1"`})
	if err != nil || !resp.NotModified {
		t.Errorf("expected 304, got %+v %v", resp, err)
	}
}

func TestDenyCIDRs(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer s.Close()
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("HTTP_PROXY", "")
	c, err := New(config.HTTP{UserAgent: "t", MaxBodyBytes: 1024, DenyCIDRs: []string{"127.0.0.0/8"}}, api(s.URL, nil))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), Request{URL: s.URL})
	fe, ok := err.(*Error)
	if !ok || fe.Kind != KindDenied {
		t.Fatalf("expected denied error, got %v", err)
	}
	// hostname resolving to loopback is caught at dial time
	_, err = c.Get(context.Background(), Request{URL: "http://localhost:1/x"})
	if fe, ok := err.(*Error); !ok || fe.Kind != KindDenied {
		t.Fatalf("expected denied error for localhost, got %v", err)
	}
}
