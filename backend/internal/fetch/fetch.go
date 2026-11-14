// Package fetch is the only place where Kansabooj performs outbound HTTP calls.
// It handles the corporate proxy, internal CA, authentication (confined to the
// configured origin), conditional requests, size limits and the deny_cidrs guard.
package fetch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"time"

	"git.k3nny.fr/kansabooj/internal/config"
)

// ErrorKind classifies fetch failures so the UI can tell the user what to do.
type ErrorKind string

const (
	KindNetwork  ErrorKind = "network"
	KindHTTP     ErrorKind = "http"
	KindAuth     ErrorKind = "auth"
	KindDenied   ErrorKind = "denied"
	KindTooLarge ErrorKind = "too_large"
)

type Error struct {
	Kind   ErrorKind
	Status int
	Msg    string
	Err    error
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.Err }

// IsAuth reports whether err is an authentication/authorization failure.
func IsAuth(err error) bool {
	var fe *Error
	return errors.As(err, &fe) && fe.Kind == KindAuth
}

type Request struct {
	URL          string
	ETag         string
	LastModified string
}

type Response struct {
	URL          string // final URL after redirects
	Status       int
	NotModified  bool
	Body         []byte
	ContentType  string
	ETag         string
	LastModified string
}

// Client fetches documents for one API.
type Client struct {
	http      *http.Client
	deny      []*net.IPNet
	allow     []*net.IPNet // when non-empty, only these ranges are reachable
	maxBody   int64
	userAgent string
	direct    bool // no proxy: the dialer enforces deny_cidrs itself
	authHint  string
}

// New builds a client for api using the HTTP settings of its network profile.
func New(global config.HTTP, api *config.API) (*Client, error) {
	h := global.ForProfile(api.Network)
	deny, err := parseCIDRs(h.DenyCIDRs)
	if err != nil {
		return nil, err
	}
	allow, err := parseCIDRs(h.AllowCIDRs)
	if err != nil {
		return nil, err
	}

	tlsCfg, err := tlsConfig(h)
	if err != nil {
		return nil, err
	}
	if api.Auth != nil && api.Auth.Type == "mtls" {
		cert, err := tls.LoadX509KeyPair(api.Auth.CertFile, api.Auth.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("chargement du certificat client mTLS : %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	proxy, direct, err := proxyFunc(h.Proxy)
	if err != nil {
		return nil, err
	}

	c := &Client{deny: deny, allow: allow, maxBody: h.MaxBodyBytes, userAgent: h.UserAgent, direct: direct}

	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if direct && (len(deny) > 0 || len(allow) > 0) {
		// Without a proxy we connect to the target ourselves: check the IP actually dialed,
		// which also defeats DNS rebinding between the pre-check and the connection.
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, _ := net.SplitHostPort(address)
			if ip := net.ParseIP(host); ip != nil && c.denied(ip) {
				return &Error{Kind: KindDenied, Msg: fmt.Sprintf("connexion refusée vers %s (politique réseau du profil %q : deny_cidrs / allow_cidrs)", ip, api.Network)}
			}
			return nil
		}
	}

	base := &http.Transport{
		Proxy:                 proxy,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       tlsCfg,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   2,
	}

	var rt http.RoundTripper = base
	if api.Auth != nil && api.Auth.Type != "mtls" {
		apply, hint, err := authenticator(api.Auth, &http.Client{Transport: base, Timeout: 30 * time.Second})
		if err != nil {
			return nil, err
		}
		rt = &originAuth{base: base, origin: api.Origin(), apply: apply}
		c.authHint = hint
	}

	c.http = &http.Client{
		Transport: rt,
		Timeout:   api.Timeout.Std(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("trop de redirections")
			}
			return c.checkTarget(req.Context(), req.URL)
		},
	}
	return c, nil
}

func tlsConfig(h config.HTTP) (*tls.Config, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if h.CABundle != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		pem, err := os.ReadFile(h.CABundle)
		if err != nil {
			return nil, fmt.Errorf("lecture de http.ca_bundle : %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("http.ca_bundle %s ne contient aucun certificat PEM", h.CABundle)
		}
		tlsCfg.RootCAs = pool
	}
	return tlsCfg, nil
}

// OutboundTransport is the transport for other outbound calls (webhooks): same
// proxy and CA as spec fetches, but no deny_cidrs since webhook targets are
// configured by operators and may legitimately be internal.
func OutboundTransport(h config.HTTP) (*http.Transport, error) {
	tlsCfg, err := tlsConfig(h)
	if err != nil {
		return nil, err
	}
	proxy, _, err := proxyFunc(h.Proxy)
	if err != nil {
		return nil, err
	}
	return &http.Transport{Proxy: proxy, TLSClientConfig: tlsCfg, ForceAttemptHTTP2: true,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second, IdleConnTimeout: 90 * time.Second}, nil
}

// Get fetches req.URL, honoring conditional headers.
func (c *Client) Get(ctx context.Context, req Request) (*Response, error) {
	u, err := url.Parse(req.URL)
	if err != nil {
		return nil, &Error{Kind: KindNetwork, Msg: fmt.Sprintf("URL invalide %q", req.URL), Err: err}
	}
	if err := c.checkTarget(ctx, u); err != nil {
		return nil, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	hr.Header.Set("User-Agent", c.userAgent)
	hr.Header.Set("Accept", "application/json, application/yaml, application/x-yaml, text/yaml, text/html;q=0.8, */*;q=0.5")
	if req.ETag != "" {
		hr.Header.Set("If-None-Match", req.ETag)
	}
	if req.LastModified != "" {
		hr.Header.Set("If-Modified-Since", req.LastModified)
	}

	resp, err := c.http.Do(hr)
	if err != nil {
		var fe *Error
		if errors.As(err, &fe) {
			return nil, fe
		}
		return nil, &Error{Kind: KindNetwork, Msg: fmt.Sprintf("échec de connexion à %s : %v", u.Host, err), Err: err}
	}
	defer resp.Body.Close()

	out := &Response{
		URL:          resp.Request.URL.String(),
		Status:       resp.StatusCode,
		ContentType:  resp.Header.Get("Content-Type"),
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}
	switch {
	case resp.StatusCode == http.StatusNotModified:
		out.NotModified = true
		return out, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		msg := fmt.Sprintf("accès refusé (%d) par %s", resp.StatusCode, resp.Request.URL.Host)
		if c.authHint != "" {
			msg += " — vérifier " + c.authHint
		} else {
			msg += " — cette source nécessite probablement une section auth"
		}
		return nil, &Error{Kind: KindAuth, Status: resp.StatusCode, Msg: msg}
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, &Error{Kind: KindHTTP, Status: resp.StatusCode, Msg: fmt.Sprintf("réponse HTTP %d de %s", resp.StatusCode, resp.Request.URL)}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return nil, &Error{Kind: KindNetwork, Msg: fmt.Sprintf("lecture de la réponse interrompue : %v", err), Err: err}
	}
	if int64(len(body)) > c.maxBody {
		return nil, &Error{Kind: KindTooLarge, Msg: fmt.Sprintf("document trop volumineux (> %d octets, voir http.max_body_bytes)", c.maxBody)}
	}
	out.Body = body
	return out, nil
}

// checkTarget applies deny_cidrs to a URL before connecting. Behind a proxy the DNS
// is resolved by the proxy, so this is best effort: a name that does not resolve
// locally is allowed and left to the proxy policy.
func (c *Client) checkTarget(ctx context.Context, u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return &Error{Kind: KindDenied, Msg: fmt.Sprintf("schéma %q non autorisé", u.Scheme)}
	}
	if len(c.deny) == 0 && len(c.allow) == 0 {
		return nil
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if c.denied(ip) {
			return &Error{Kind: KindDenied, Msg: fmt.Sprintf("%s n'est pas autorisé par la politique réseau (deny_cidrs / allow_cidrs)", host)}
		}
		return nil
	}
	if c.direct {
		return nil // enforced at dial time
	}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(rctx, host)
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if c.denied(a.IP) {
			return &Error{Kind: KindDenied, Msg: fmt.Sprintf("%s résout vers %s, non autorisé par la politique réseau (deny_cidrs / allow_cidrs)", host, a.IP)}
		}
	}
	return nil
}

func (c *Client) denied(ip net.IP) bool {
	for _, n := range c.deny {
		if n.Contains(ip) {
			return true
		}
	}
	if len(c.allow) == 0 {
		return false
	}
	for _, n := range c.allow {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// proxyFunc returns the proxy selector for a setting: "" = environment
// (HTTPS_PROXY…), "none" = direct connection, otherwise a proxy URL. direct
// reports whether connections go straight to the target.
func proxyFunc(setting string) (func(*http.Request) (*url.URL, error), bool, error) {
	switch setting {
	case "none":
		return nil, true, nil
	case "":
		direct := os.Getenv("HTTPS_PROXY") == "" && os.Getenv("https_proxy") == "" &&
			os.Getenv("HTTP_PROXY") == "" && os.Getenv("http_proxy") == ""
		return http.ProxyFromEnvironment, direct, nil
	}
	pu, err := url.Parse(setting)
	if err != nil || pu.Host == "" {
		return nil, false, fmt.Errorf("proxy invalide %q", setting)
	}
	return http.ProxyURL(pu), false, nil
}

func parseCIDRs(list []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(list))
	for _, s := range list {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return nil, fmt.Errorf("deny_cidrs : %w", err)
		}
		out = append(out, n)
	}
	return out, nil
}
