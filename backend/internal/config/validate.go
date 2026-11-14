package config

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// ValidationError lists every problem found in the configuration.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return fmt.Sprintf("configuration invalide (%d problème(s)) :\n  - %s", len(e.Problems), strings.Join(e.Problems, "\n  - "))
}

var (
	idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	opPattern = regexp.MustCompile(`^(GET|PUT|POST|DELETE|PATCH|HEAD|OPTIONS|TRACE) /\S*$`)

	sourceTypes   = []string{SourceOpenAPI, SourceSwaggerUI, SourceRedoc, SourceHTML, SourceAuto}
	notifierTypes = []string{"googlechat", "slack", "teams", "generic"}
	levels        = []string{"error", "warn", "info"}
)

func validate(c *Config) []string {
	var p []string
	add := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	if c.HTTP.Proxy != "" {
		if u, err := url.Parse(c.HTTP.Proxy); err != nil || u.Host == "" {
			add("http.proxy : URL de proxy invalide %q", c.HTTP.Proxy)
		}
	}
	for _, cidr := range c.HTTP.DenyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			add("http.deny_cidrs : CIDR invalide %q", cidr)
		}
	}

	notifiers := map[string]bool{}
	for i, n := range c.Notifiers {
		where := fmt.Sprintf("notifiers[%d] (%s)", i, n.Name)
		if n.Name == "" {
			add("notifiers[%d] : champ name obligatoire", i)
		} else if notifiers[n.Name] {
			add("%s : nom de notifier en double", where)
		}
		notifiers[n.Name] = true
		if !slices.Contains(notifierTypes, n.Type) {
			add("%s : type %q inconnu (valeurs possibles : %s)", where, n.Type, strings.Join(notifierTypes, ", "))
		}
		if !isHTTPURL(n.URL) {
			add("%s : url absente ou invalide", where)
		}
		if !slices.Contains(levels, n.MinLevel) {
			add("%s : min_level %q inconnu (valeurs possibles : %s)", where, n.MinLevel, strings.Join(levels, ", "))
		}
	}
	for _, name := range c.Defaults.Notify {
		if !notifiers[name] {
			add("defaults.notify : notifier %q non déclaré dans notifiers", name)
		}
	}

	if len(c.APIs) == 0 {
		add("apis : aucune API déclarée")
	}
	ids := map[string]bool{}
	for i, a := range c.APIs {
		where := fmt.Sprintf("apis[%d] (%s)", i, a.ID)
		switch {
		case a.ID == "":
			add("apis[%d] : champ id obligatoire", i)
		case !idPattern.MatchString(a.ID):
			add("%s : id invalide (minuscules, chiffres et tirets uniquement)", where)
		case ids[a.ID]:
			add("%s : id en double", where)
		}
		ids[a.ID] = true
		if a.Name == "" {
			add("%s : champ name obligatoire", where)
		}
		if !isHTTPURL(a.Source.URL) {
			add("%s : source.url absente ou invalide (http/https attendu)", where)
		}
		if !slices.Contains(sourceTypes, a.Source.Type) {
			add("%s : source.type %q inconnu (valeurs possibles : %s)", where, a.Source.Type, strings.Join(sourceTypes, ", "))
		}
		for _, name := range a.Notify {
			if !notifiers[name] {
				add("%s : notifier %q non déclaré dans notifiers", where, name)
			}
		}
		for _, op := range a.Gateway.ExposedOperations {
			if !opPattern.MatchString(op) {
				add("%s : gateway.exposed_operations %q invalide (format attendu : \"GET /chemin/{param}\")", where, op)
			}
		}
		for _, ig := range a.Ignore {
			if _, err := ParseIgnorePath(ig); err != nil {
				add("%s : ignore %q : %v", where, ig, err)
			}
		}
		if a.Auth != nil {
			for _, msg := range validateAuth(a.Auth) {
				add("%s : auth : %s", where, msg)
			}
		}
	}
	return p
}

func validateAuth(a *Auth) []string {
	var p []string
	need := func(field, v string) {
		if v == "" {
			p = append(p, fmt.Sprintf("champ %s obligatoire pour le type %s", field, a.Type))
		}
	}
	switch a.Type {
	case "header", "query":
		need("name", a.Name)
		need("value", a.Value)
	case "basic":
		need("username", a.Username)
		need("password", a.Password)
	case "bearer":
		need("token", a.Token)
	case "oauth2_client_credentials":
		need("token_url", a.TokenURL)
		need("client_id", a.ClientID)
		need("client_secret", a.ClientSecret)
		if a.TokenURL != "" && !isHTTPURL(a.TokenURL) {
			p = append(p, "token_url invalide")
		}
	case "mtls":
		need("cert_file", a.CertFile)
		need("key_file", a.KeyFile)
	default:
		p = append(p, fmt.Sprintf("type %q inconnu (header, query, basic, bearer, oauth2_client_credentials, mtls)", a.Type))
	}
	return p
}

func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// ParseIgnorePath parses the supported JSONPath subset: $.a.b, with * matching any key.
func ParseIgnorePath(p string) ([]string, error) {
	rest, ok := strings.CutPrefix(p, "$.")
	if !ok || rest == "" {
		return nil, fmt.Errorf("doit commencer par \"$.\" (ex : $.servers, $.paths.*.x-internal)")
	}
	parts := strings.Split(rest, ".")
	for _, s := range parts {
		if s == "" {
			return nil, fmt.Errorf("segment vide")
		}
	}
	return parts, nil
}
