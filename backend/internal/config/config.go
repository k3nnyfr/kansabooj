// Package config loads, expands and validates the apis.yml configuration file.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
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

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

// Std returns the value as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

type Config struct {
	Defaults  Defaults   `yaml:"defaults"`
	HTTP      HTTP       `yaml:"http"`
	Notifiers []Notifier `yaml:"notifiers"`
	APIs      []API      `yaml:"apis"`
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
	Proxy        string   `yaml:"proxy"`
	CABundle     string   `yaml:"ca_bundle"`
	UserAgent    string   `yaml:"user_agent"`
	DenyCIDRs    []string `yaml:"deny_cidrs"`
	MaxBodyBytes int64    `yaml:"max_body_bytes"`
}

type Notifier struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	URL         string `yaml:"url"`
	Secret      string `yaml:"secret"`
	MinLevel    string `yaml:"min_level"`
	OnlyInScope *bool  `yaml:"only_in_scope"`
}

type API struct {
	ID       string    `yaml:"id" json:"id"`
	Name     string    `yaml:"name" json:"name"`
	Owner    string    `yaml:"owner" json:"owner,omitempty"`
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

func applyDefaults(c *Config) {
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
		if n.OnlyInScope == nil {
			t := true
			n.OnlyInScope = &t
		}
	}
	for i := range c.APIs {
		a := &c.APIs[i]
		if a.Source.Type == "" {
			a.Source.Type = SourceAuto
		}
		if a.Interval == nil {
			v := d.Interval
			a.Interval = &v
		}
		if a.Timeout == nil {
			v := d.Timeout
			a.Timeout = &v
		}
		if a.Notify == nil {
			a.Notify = d.Notify
		}
		a.Ignore = append(append([]string{}, d.Ignore...), a.Ignore...)
	}
}

// Load reads, expands ${...} references, applies defaults and validates the file.
// All validation problems are returned at once as a *ValidationError.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("lecture de la configuration : %w", err)
	}
	return Parse(raw)
}

// Parse is Load without the file read.
func Parse(raw []byte) (*Config, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("YAML invalide : %w", err)
	}
	problems := unknownFields(raw)
	expandNode(&root, &problems)

	var c Config
	if err := root.Decode(&c); err != nil {
		return nil, fmt.Errorf("structure de configuration invalide : %w", err)
	}
	applyDefaults(&c)
	problems = append(problems, validate(&c)...)
	if len(problems) > 0 {
		return &c, &ValidationError{Problems: problems}
	}
	return &c, nil
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
