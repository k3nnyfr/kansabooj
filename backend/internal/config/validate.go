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
	idPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	hashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	tagPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_.:-]{0,49}$`)
	opPattern   = regexp.MustCompile(`^(GET|PUT|POST|DELETE|PATCH|HEAD|OPTIONS|TRACE) /\S*$`)

	sourceTypes   = []string{SourceOpenAPI, SourceSwaggerUI, SourceRedoc, SourceHTML, SourceAuto}
	notifierTypes = []string{"googlechat", "slack", "teams", "generic"}
	levels        = []string{"error", "warn", "info"}
)

func validate(c *Config) []string {
	var p []string
	add := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	p = append(p, c.Database.validate()...)
	if !slices.Contains(Languages, c.Server.Language) {
		add("server.language : langue %q non supportée (fr, en)", c.Server.Language)
	}
	if c.Server.BaseURL != "" && !isHTTPURL(c.Server.BaseURL) {
		add("server.base_url : URL invalide %q", c.Server.BaseURL)
	}
	tokNames := map[string]bool{}
	for i, tk := range c.Server.Tokens {
		where := fmt.Sprintf("server.tokens[%d] (%s)", i, tk.Name)
		if tk.Name == "" || tokNames[tk.Name] {
			add("%s : name absent ou en double", where)
		}
		tokNames[tk.Name] = true
		if !hashPattern.MatchString(tk.Hash) {
			add("%s : hash attendu au format sha256:<64 hexa> (générer avec « kansabooj token new »)", where)
		}
		if len(tk.Scopes) == 0 {
			add("%s : au moins un scope (%s)", where, strings.Join(Scopes, ", "))
		}
		for _, sc := range tk.Scopes {
			if !slices.Contains(Scopes, sc) {
				add("%s : scope %q inconnu (%s)", where, sc, strings.Join(Scopes, ", "))
			}
		}
	}
	if c.Server.Concurrency < 1 {
		add("server.concurrency : doit être ≥ 1")
	}
	if c.HTTP.Proxy != "" {
		if u, err := url.Parse(c.HTTP.Proxy); err != nil || u.Host == "" {
			add("http.proxy : URL de proxy invalide %q", c.HTTP.Proxy)
		}
	}
	checkCIDRs := func(where string, list []string) {
		for _, cidr := range list {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				add("%s : CIDR invalide %q", where, cidr)
			}
		}
	}
	checkCIDRs("http.deny_cidrs", c.HTTP.DenyCIDRs)
	checkCIDRs("http.allow_cidrs", c.HTTP.AllowCIDRs)
	for name, prof := range c.HTTP.Profiles {
		where := "http.profiles." + name
		if name == DefaultProfile {
			add("%s : nom réservé (le profil par défaut correspond aux réglages http de premier niveau)", where)
		}
		if prof.Proxy != "" && prof.Proxy != "none" {
			if u, err := url.Parse(prof.Proxy); err != nil || u.Host == "" {
				add("%s.proxy : \"none\" ou URL de proxy attendue, %q reçu", where, prof.Proxy)
			}
		}
		checkCIDRs(where+".deny_cidrs", prof.DenyCIDRs)
		checkCIDRs(where+".allow_cidrs", prof.AllowCIDRs)
	}
	profileExists := func(name string) bool {
		_, ok := c.HTTP.Profiles[name]
		return name == "" || name == DefaultProfile || ok
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
		if !slices.Contains(Languages, n.Language) {
			add("%s : language %q non supportée (fr, en)", where, n.Language)
		}
		if !slices.Contains(levels, n.MinLevel) {
			add("%s : min_level %q inconnu (valeurs possibles : %s)", where, n.MinLevel, strings.Join(levels, ", "))
		}
		if n.Digest != nil {
			n.Digest.validate(where, add)
		}
	}
	groups := map[string]bool{}
	for i, g := range c.Groups {
		where := fmt.Sprintf("groups[%d] (%s)", i, g.ID)
		if g.File != "" {
			where = fmt.Sprintf("%s : groupe %s", g.File, g.ID)
		}
		switch {
		case !idPattern.MatchString(g.ID):
			add("groups[%d] : id absent ou invalide (minuscules, chiffres et tirets)", i)
		case groups[g.ID]:
			add("%s : id de groupe en double", where)
		}
		groups[g.ID] = true
		if g.Name == "" {
			add("%s : champ name obligatoire", where)
		}
		for _, name := range g.Notify {
			if !notifiers[name] {
				add("%s : notifier %q non déclaré dans notifiers", where, name)
			}
		}
		for _, tag := range g.Tags {
			if !tagPattern.MatchString(tag) {
				add("%s : tag %q invalide (minuscules, chiffres, - _ . :)", where, tag)
			}
		}
		if !profileExists(g.Network) {
			add("%s : profil réseau %q non déclaré dans http.profiles", where, g.Network)
		}
		if g.Auth != nil {
			for _, msg := range validateAuth(g.Auth) {
				add("%s : auth : %s", where, msg)
			}
		}
	}
	for i, n := range c.Notifiers {
		if n.Subscribe == nil {
			continue
		}
		for _, g := range n.Subscribe.Groups {
			if !groups[g] {
				add("notifiers[%d] (%s) : subscribe.groups : groupe %q inconnu", i, n.Name, g)
			}
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
	ids := map[string]string{} // id → where it was first declared
	for i, a := range c.APIs {
		where := fmt.Sprintf("apis[%d] (%s)", i, a.ID)
		if a.File != "" {
			where = fmt.Sprintf("%s : api %s", a.File, a.ID)
		}
		switch {
		case a.ID == "":
			add("apis[%d] : champ id obligatoire", i)
		case !idPattern.MatchString(a.ID):
			add("%s : id invalide (minuscules, chiffres et tirets uniquement)", where)
		case ids[a.ID] != "":
			add("%s : id en double (déjà déclaré : %s)", where, ids[a.ID])
		}
		if ids[a.ID] == "" {
			ids[a.ID] = where
		}
		if a.Name == "" {
			add("%s : champ name obligatoire", where)
		}
		if a.Group != "" && !groups[a.Group] {
			add("%s : groupe %q non déclaré dans groups", where, a.Group)
		}
		for _, tag := range a.Tags {
			if !tagPattern.MatchString(tag) {
				add("%s : tag %q invalide (minuscules, chiffres, - _ . :)", where, tag)
			}
		}
		if !profileExists(a.Network) {
			add("%s : profil réseau %q non déclaré dans http.profiles", where, a.Network)
		}
		if !isHTTPURL(a.Source.URL) {
			add("%s : source.url absente ou invalide (http/https attendu)", where)
		}
		for _, expr := range a.Source.TextIgnore {
			if _, err := regexp.Compile(expr); err != nil {
				add("%s : source.text_ignore %q : expression régulière invalide (%v)", where, expr, err)
			}
		}
		if len(a.Source.TextIgnore) > 0 && a.Source.Type != SourceHTML && a.Source.Type != SourceAuto {
			add("%s : source.text_ignore ne s'applique qu'aux types html et auto", where)
		}
		if a.Source.SpecName != "" && a.Source.Type == SourceOpenAPI || a.Source.SpecName != "" && a.Source.Type == SourceHTML {
			add("%s : source.spec_name ne s'applique qu'aux types swagger-ui, redoc et auto", where)
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
		if g, _ := c.Group(a.Group); a.Auth != nil && (g == nil || a.Auth != g.Auth) { // inherited auth is reported once, on the group
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

// IgnoreDescendant is the path segment produced by "..": the rest of the path is matched
// at any depth (including inside arrays).
const IgnoreDescendant = "**"

// ParseIgnorePath parses the supported JSONPath subset: $.a.b, with * matching any key,
// and $..a (or $.a..b) matching a key at any depth below.
func ParseIgnorePath(p string) ([]string, error) {
	rest, ok := strings.CutPrefix(p, "$")
	if !ok || rest == "" || rest[0] != '.' {
		return nil, fmt.Errorf("doit commencer par \"$.\" ou \"$..\" (ex : $.servers, $.paths.*.x-internal, $..example)")
	}
	var parts []string
	for rest != "" {
		if strings.HasPrefix(rest, "..") {
			parts, rest = append(parts, IgnoreDescendant), rest[2:]
		} else {
			rest = rest[1:] // "."
		}
		name, after, _ := strings.Cut(rest, ".")
		if name == "" {
			return nil, fmt.Errorf("segment vide")
		}
		if name == "*" && len(parts) > 0 && parts[len(parts)-1] == IgnoreDescendant {
			return nil, fmt.Errorf("\"..*\" retirerait tout le document : nommer la clé (ex : $..example)")
		}
		parts = append(parts, name)
		if after == "" && strings.HasSuffix(rest, ".") {
			return nil, fmt.Errorf("segment vide")
		}
		rest = rest[len(name):]
	}
	return parts, nil
}
