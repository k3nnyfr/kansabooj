// Package config loads, expands and validates the apis.yml configuration file.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
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

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	*d = Duration(v)
	return err
}

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
	// Include lists glob patterns of fragment files (relative to the main file) each
	// declaring `groups` and `apis` — typically one file per team (apis.d/*.yml).
	Include []string `yaml:"include"`
	baseDir string

	// Files lists the files the configuration was built from (the YAML itself and
	// every ${file:...} secret), so they can be watched for changes.
	Files []string `yaml:"-"`

	Server    Server     `yaml:"server"`
	Database  Database   `yaml:"database"`
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
	File        string    `yaml:"-" json:"-"` // fragment the group comes from ("" = main file)
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

	// RequireIdentity rejects API calls carrying neither an SSO identity (UserHeader)
	// nor a valid token. When false, anonymous callers get every right (the reverse
	// proxy is then responsible for access control).
	RequireIdentity bool     `yaml:"require_identity"`
	Admins          []string `yaml:"admins"` // SSO users allowed to call admin endpoints when require_identity is set
	Tokens          []Token  `yaml:"tokens"`
}

// Token is a machine credential (CI, integrations). Only its hash is configured;
// generate one with `kansabooj token new <name>`.
type Token struct {
	Name   string   `yaml:"name"`
	Hash   string   `yaml:"hash"`   // sha256:<hex>
	Scopes []string `yaml:"scopes"` // read | check | ack | validate | admin
}

// Scopes known to the API.
var Scopes = []string{"read", "check", "ack", "validate", "admin"}

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
	File     string    `yaml:"-" json:"-"` // fragment the API comes from ("" = main file)
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
	c.Database.applyDefaults()
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

// Load reads the configuration file and the fragments it includes, expands ${...}
// references, applies defaults and validates the result. All problems are
// returned at once as a *ValidationError.
func Load(path string) (*Config, error) { return LoadWith(path, nil) }

// LoadWith is Load where some fragments (keyed by base name) are replaced by, or
// complemented with, the given contents — used to validate a proposed change of a
// team's fragment against the live configuration.
func LoadWith(path string, overrides map[string][]byte) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("lecture de la configuration : %w", err)
	}
	c, problems, err := decodeMain(raw, "")
	if err != nil {
		return nil, err
	}
	c.Files = append([]string{path}, c.Files...)
	c.baseDir = filepath.Dir(path)

	frags, err := c.fragmentFiles()
	if err != nil {
		problems = append(problems, err.Error())
	}
	seen := map[string]bool{}
	for _, f := range frags {
		name := filepath.Base(f)
		seen[name] = true
		body, ok := overrides[name]
		if !ok {
			if body, err = os.ReadFile(f); err != nil {
				problems = append(problems, fmt.Sprintf("%s : lecture impossible : %v", name, err))
				continue
			}
			c.Files = append(c.Files, f)
		}
		problems = append(problems, c.addFragment(name, body)...)
	}
	for name, body := range overrides {
		if !seen[name] {
			problems = append(problems, c.addFragment(name, body)...)
		}
	}
	return finish(c, problems)
}

// Parse builds a configuration from a single document (includes are not resolved).
func Parse(raw []byte) (*Config, error) {
	c, problems, err := decodeMain(raw, "")
	if err != nil {
		return nil, err
	}
	return finish(c, problems)
}

func finish(c *Config, problems []string) (*Config, error) {
	applyDefaults(c)
	problems = append(problems, validate(c)...)
	if len(problems) > 0 {
		return c, &ValidationError{Problems: problems}
	}
	return c, nil
}

// decodeMain parses and expands the main document.
func decodeMain(raw []byte, origin string) (*Config, []string, error) {
	var c Config
	problems, files, err := decodeDoc(raw, origin, &c)
	if err != nil {
		return nil, nil, err
	}
	c.Files = files
	return &c, problems, nil
}

// fragment is what an included file may declare: the APIs and groups of a team.
// Notifiers, network profiles and server settings stay in the main file.
type fragment struct {
	Groups []Group `yaml:"groups"`
	APIs   []API   `yaml:"apis"`
}

func (c *Config) addFragment(name string, raw []byte) []string {
	var f fragment
	problems, files, err := decodeDoc(raw, name, &f)
	if err != nil {
		return append(problems, err.Error())
	}
	c.Files = append(c.Files, files...)
	for i := range f.Groups {
		f.Groups[i].File = name
	}
	for i := range f.APIs {
		f.APIs[i].File = name
	}
	c.Groups = append(c.Groups, f.Groups...)
	c.APIs = append(c.APIs, f.APIs...)
	return problems
}

// fragmentFiles resolves the include patterns (relative to the main file).
func (c *Config) fragmentFiles() ([]string, error) {
	var out []string
	for _, pattern := range c.Include {
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(c.baseDir, pattern)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return out, fmt.Errorf("include %q : motif invalide : %v", pattern, err)
		}
		sort.Strings(matches)
		for _, m := range matches {
			if !slices.Contains(out, m) {
				out = append(out, m)
			}
		}
	}
	return out, nil
}

// Fingerprint changes whenever the main file, an included fragment (added,
// removed or edited) or a referenced secret file changes.
func (c *Config) Fingerprint() string {
	h := sha256.New()
	files := append([]string{}, c.Files...)
	if frags, err := c.fragmentFiles(); err == nil {
		files = append(files, frags...)
	}
	sort.Strings(files)
	for _, f := range slices.Compact(files) {
		b, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(h, "%s:missing;", f)
			continue
		}
		fmt.Fprintf(h, "%s:%x;", f, sha256.Sum256(b))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// decodeDoc parses one YAML document into out: unknown keys, ${...} expansion
// (after parsing, so secrets cannot break the YAML) and decoding. Problems are
// prefixed with origin when set.
func decodeDoc(raw []byte, origin string, out any) (problems, files []string, err error) {
	prefix := ""
	if origin != "" {
		prefix = origin + " : "
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, nil, fmt.Errorf("%sYAML invalide : %w", prefix, err)
	}
	var p []string
	for _, msg := range unknownFields(raw, out) {
		p = append(p, prefix+msg)
	}
	var exp []string
	expandNode(&root, &exp, &files)
	for _, msg := range exp {
		p = append(p, prefix+msg)
	}
	if len(root.Content) == 0 {
		return p, files, nil // empty document
	}
	if err := root.Decode(out); err != nil {
		return p, files, fmt.Errorf("%sstructure invalide : %w", prefix, err)
	}
	return p, files, nil
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
func unknownFields(raw []byte, shape any) []string {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	target := reflect.New(reflect.TypeOf(shape).Elem()).Interface() // fresh value of the same type
	var te *yaml.TypeError
	if err := dec.Decode(target); errors.As(err, &te) {
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
