// Package config loads, expands and validates the apis.yml configuration file.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration decoded from a Go duration string ("6h", "30s").
type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("ligne %d : durée invalide %q (exemples : 30s, 10m, 6h)", n.Line, n.Value)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// String formats the duration without zero units: 6h, 1h30m, 45s.
func (d Duration) String() string {
	s := time.Duration(d).String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// Std returns the value as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

type Config struct {
	// Files lists the files the configuration was built from (the YAML itself and
	// every ${file:...} secret), so they can be watched for changes.
	Files []string `yaml:"-"`

	Server    Server     `yaml:"server"`
	Defaults  Defaults   `yaml:"defaults"`
	HTTP      HTTP       `yaml:"http"`
	Notifiers []Notifier `yaml:"notifiers"`
	Groups    []Group    `yaml:"groups"`
	APIs      []API      `yaml:"apis"`
}

// Group gathers APIs (a partner, a domain, internal vs external…) and carries
// default values inherited by its APIs. API values win over group values, which
// win over `defaults`.
type Group struct {
	ID          string    `yaml:"id" json:"id"`
	Name        string    `yaml:"name" json:"name"`
	Description string    `yaml:"description" json:"description,omitempty"`
	Owner       string    `yaml:"owner" json:"owner,omitempty"`
	Tags        []string  `yaml:"tags" json:"tags,omitempty"`
	Interval    *Duration `yaml:"interval" json:"-"`
	Timeout     *Duration `yaml:"timeout" json:"-"`
	Notify      []string  `yaml:"notify" json:"-"`
	Ignore      []string  `yaml:"ignore" json:"-"`
	Auth        *Auth     `yaml:"auth" json:"-"`
	Network     string    `yaml:"network" json:"network,omitempty"`
}

// NetworkProfile overrides the outbound HTTP settings for the APIs using it, e.g.
// internal APIs reached without the corporate proxy.
type NetworkProfile struct {
	Proxy      string   `yaml:"proxy"`       // "" = inherit http.proxy, "none" = direct connection, or a URL
	CABundle   string   `yaml:"ca_bundle"`   // "" = inherit http.ca_bundle
	DenyCIDRs  []string `yaml:"deny_cidrs"`  // absent = inherit http.deny_cidrs, [] = none
	AllowCIDRs []string `yaml:"allow_cidrs"` // non-empty = targets must be inside these ranges
}

// DefaultProfile is the implicit profile made of the top-level http settings.
const DefaultProfile = "default"

// Subscription routes the notifications of matching APIs to a notifier, in
// addition to the notifiers listed by the APIs themselves.
type Subscription struct {
	Groups []string `yaml:"groups"`
	Tags   []string `yaml:"tags"`
}

// Server holds the settings of the `serve` command.
type Server struct {
	Listen         string   `yaml:"listen"`          // default :8080
	BaseURL        string   `yaml:"base_url"`        // public URL of the UI, used for links in notifications
	Language       string   `yaml:"language"`        // fr | en: default UI and notification language
	UserHeader     string   `yaml:"user_header"`     // header set by the SSO reverse proxy (default X-Forwarded-User)
	Concurrency    int      `yaml:"concurrency"`     // parallel checks (default 4)
	CheckRetention Duration `yaml:"check_retention"` // how long check logs are kept (default 90 days)
}

type Defaults struct {
	Interval       Duration `yaml:"interval"`
	Jitter         Duration `yaml:"jitter"`
	Timeout        Duration `yaml:"timeout"`
	ErrorThreshold int      `yaml:"error_threshold"`
	Notify         []string `yaml:"notify"`
	Ignore         []string `yaml:"ignore"`
}

type HTTP struct {
	Proxy        string                    `yaml:"proxy"`
	CABundle     string                    `yaml:"ca_bundle"`
	UserAgent    string                    `yaml:"user_agent"`
	DenyCIDRs    []string                  `yaml:"deny_cidrs"`
	AllowCIDRs   []string                  `yaml:"allow_cidrs"`
	MaxBodyBytes int64                     `yaml:"max_body_bytes"`
	Profiles     map[string]NetworkProfile `yaml:"profiles"`
}

// ForProfile returns the effective HTTP settings for a network profile.
func (h HTTP) ForProfile(name string) HTTP {
	p, ok := h.Profiles[name]
	if name == "" || name == DefaultProfile || !ok {
		return h
	}
	out := h
	if p.Proxy != "" {
		out.Proxy = p.Proxy
	}
	if p.CABundle != "" {
		out.CABundle = p.CABundle
	}
	if p.DenyCIDRs != nil {
		out.DenyCIDRs = p.DenyCIDRs
	}
	if p.AllowCIDRs != nil {
		out.AllowCIDRs = p.AllowCIDRs
	}
	return out
}

type Notifier struct {
	Name        string        `yaml:"name"`
	Type        string        `yaml:"type"`
	URL         string        `yaml:"url"`
	Secret      string        `yaml:"secret"`
	MinLevel    string        `yaml:"min_level"`
	OnlyInScope *bool         `yaml:"only_in_scope"`
	Language    string        `yaml:"language"` // fr | en, defaults to server.language
	Subscribe   *Subscription `yaml:"subscribe"`
}

// Subscribed reports whether the notifier subscribed to the API's group or tags.
func (n *Notifier) Subscribed(a *API) bool {
	if n.Subscribe == nil {
		return false
	}
	if a.Group != "" && slices.Contains(n.Subscribe.Groups, a.Group) {
		return true
	}
	for _, t := range a.Tags {
		if slices.Contains(n.Subscribe.Tags, t) {
			return true
		}
	}
	return false
}

type API struct {
	ID       string    `yaml:"id" json:"id"`
	Name     string    `yaml:"name" json:"name"`
	Owner    string    `yaml:"owner" json:"owner,omitempty"`
	Group    string    `yaml:"group" json:"group,omitempty"`
	Tags     []string  `yaml:"tags" json:"tags,omitempty"`
	Network  string    `yaml:"network" json:"network,omitempty"` // network profile, default "default"
	Source   Source    `yaml:"source" json:"source"`
	Auth     *Auth     `yaml:"auth" json:"-"` // never serialized: contains secrets
	Interval *Duration `yaml:"interval" json:"interval,omitempty"`
	Timeout  *Duration `yaml:"timeout" json:"timeout,omitempty"`
	Notify   []string  `yaml:"notify" json:"notify,omitempty"`
	Ignore   []string  `yaml:"ignore" json:"ignore,omitempty"`
	Gateway  Gateway   `yaml:"gateway" json:"gateway,omitempty"`
}

type Source struct {
	URL  string `yaml:"url" json:"url"`
	Type string `yaml:"type" json:"type"`
}

type Gateway struct {
	ExposedOperations []string `yaml:"exposed_operations" json:"exposed_operations,omitempty"`
}

// Auth describes how to authenticate against a protected documentation source.
// Credentials are only ever sent to the origin of Source.URL.
type Auth struct {
	Type string `yaml:"type"` // header | query | basic | bearer | oauth2_client_credentials | mtls

	// header / query
	Name  string `yaml:"name"`
	Value string `yaml:"value"`

	// basic
	Username string `yaml:"username"`
	Password string `yaml:"password"`

	// bearer
	Token string `yaml:"token"`

	// oauth2_client_credentials
	TokenURL     string            `yaml:"token_url"`
	ClientID     string            `yaml:"client_id"`
	ClientSecret string            `yaml:"client_secret"`
	Scopes       []string          `yaml:"scopes"`
	Params       map[string]string `yaml:"params"`

	// mtls
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

const (
	SourceOpenAPI   = "openapi"
	SourceSwaggerUI = "swagger-ui"
	SourceRedoc     = "redoc"
	SourceHTML      = "html"
	SourceAuto      = "auto"
)

// Languages supported by the UI and the notifications.
var Languages = []string{"fr", "en"}

func applyDefaults(c *Config) {
	sv := &c.Server
	if sv.Listen == "" {
		sv.Listen = ":8080"
	}
	if sv.Language == "" {
		sv.Language = "fr"
	}
	if sv.UserHeader == "" {
		sv.UserHeader = "X-Forwarded-User"
	}
	if sv.Concurrency == 0 {
		sv.Concurrency = 4
	}
	if sv.CheckRetention == 0 {
		sv.CheckRetention = Duration(90 * 24 * time.Hour)
	}
	sv.BaseURL = strings.TrimRight(sv.BaseURL, "/")
	d := &c.Defaults
	if d.Interval == 0 {
		d.Interval = Duration(6 * time.Hour)
	}
	if d.Jitter == 0 {
		d.Jitter = Duration(5 * time.Minute)
	}
	if d.Timeout == 0 {
		d.Timeout = Duration(30 * time.Second)
	}
	if d.ErrorThreshold == 0 {
		d.ErrorThreshold = 3
	}
	if c.HTTP.UserAgent == "" {
		c.HTTP.UserAgent = "kansabooj/0.1"
	}
	if c.HTTP.MaxBodyBytes == 0 {
		c.HTTP.MaxBodyBytes = 20 << 20
	}
	for i := range c.Notifiers {
		n := &c.Notifiers[i]
		if n.MinLevel == "" {
			n.MinLevel = "warn"
		}
		if n.Language == "" {
			n.Language = sv.Language
		}
		if n.OnlyInScope == nil {
			t := true
			n.OnlyInScope = &t
		}
	}
	groups := map[string]*Group{}
	for i := range c.Groups {
		groups[c.Groups[i].ID] = &c.Groups[i]
	}
	for i := range c.APIs {
		a := &c.APIs[i]
		g := groups[a.Group]
		if g == nil {
			g = &Group{}
		}
		if a.Source.Type == "" {
			a.Source.Type = SourceAuto
		}
		if a.Owner == "" {
			a.Owner = g.Owner
		}
		a.Interval = firstDuration(a.Interval, g.Interval, d.Interval)
		a.Timeout = firstDuration(a.Timeout, g.Timeout, d.Timeout)
		switch {
		case a.Notify != nil:
		case g.Notify != nil:
			a.Notify = g.Notify
		default:
			a.Notify = d.Notify
		}
		if a.Auth == nil {
			a.Auth = g.Auth
		}
		if a.Network == "" {
			a.Network = g.Network
		}
		if a.Network == "" {
			a.Network = DefaultProfile
		}
		a.Ignore = append(append(append([]string{}, d.Ignore...), g.Ignore...), a.Ignore...)
		a.Tags = mergeTags(g.Tags, a.Tags)
	}
}

// Load reads, expands ${...} references, applies defaults and validates the file.
// All validation problems are returned at once as a *ValidationError.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("lecture de la configuration : %w", err)
	}
	c, err := parse(raw)
	if c != nil {
		c.Files = append([]string{path}, c.Files...)
	}
	return c, err
}

// Parse is Load without the file read.
func Parse(raw []byte) (*Config, error) { return parse(raw) }

func parse(raw []byte) (*Config, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("YAML invalide : %w", err)
	}
	problems := unknownFields(raw)
	var files []string
	expandNode(&root, &problems, &files)

	var c Config
	if err := root.Decode(&c); err != nil {
		return nil, fmt.Errorf("structure de configuration invalide : %w", err)
	}
	c.Files = files
	applyDefaults(&c)
	problems = append(problems, validate(&c)...)
	if len(problems) > 0 {
		return &c, &ValidationError{Problems: problems}
	}
	return &c, nil
}

func firstDuration(v ...any) *Duration {
	for _, x := range v {
		switch d := x.(type) {
		case *Duration:
			if d != nil {
				return d
			}
		case Duration:
			return &d
		}
	}
	return nil
}

func mergeTags(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		for _, t := range l {
			if !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Group returns the group with the given id.
func (c *Config) Group(id string) (*Group, bool) {
	for i := range c.Groups {
		if c.Groups[i].ID == id {
			return &c.Groups[i], true
		}
	}
	return nil, false
}

// unknownFields reports misspelled or unsupported keys, which yaml would otherwise silently ignore.
func unknownFields(raw []byte) []string {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var c Config
	var te *yaml.TypeError
	if err := dec.Decode(&c); errors.As(err, &te) {
		var p []string
		for _, msg := range te.Errors {
			if strings.Contains(msg, "not found in type") {
				p = append(p, "clé inconnue : "+strings.TrimPrefix(msg, "yaml: "))
			}
		}
		return p
	}
	return nil
}

// API returns the API with the given id.
func (c *Config) API(id string) (*API, bool) {
	for i := range c.APIs {
		if c.APIs[i].ID == id {
			return &c.APIs[i], true
		}
	}
	return nil, false
}

// PublicJSON returns the effective configuration of an API without any secret,
// suitable for storage and display.
func (a *API) PublicJSON() string {
	b, _ := json.Marshal(a)
	return string(b)
}

// Origin returns scheme://host[:port] of the source URL, the only origin credentials go to.
func (a *API) Origin() string {
	u, err := url.Parse(a.Source.URL)
	if err != nil {
		return ""
	}
	return OriginOf(u)
}

// OriginOf normalizes the origin of u (default ports made explicit).
func OriginOf(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Hostname()) + ":" + port
}
