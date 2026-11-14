// Package discover finds the specification file behind a documentation page
// (Swagger-UI, Redoc, springdoc, Swashbuckle…). Every request goes through the
// API's fetch client, so proxy, deny/allow lists and origin-bound credentials apply.
package discover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"git.k3nny.fr/kansabooj/internal/fetch"
	"git.k3nny.fr/kansabooj/internal/normalize"
)

// Getter is the subset of *fetch.Client used here.
type Getter interface {
	Get(ctx context.Context, req fetch.Request) (*fetch.Response, error)
}

// Result is the specification found behind a page.
type Result struct {
	SpecURL      string
	Response     *fetch.Response
	Method       string   // how it was found (for the UI and logs)
	Alternatives []string // other specs declared by the page (Swagger-UI "urls"), by name
}

// ErrNotFound means no specification could be located.
var ErrNotFound = errors.New("aucune spécification trouvée derrière cette page")

type candidate struct {
	url, name, method string
}

var (
	reRedocAttr     = regexp.MustCompile(`(?is)<redoc[^>]*\sspec-url\s*=\s*["']([^"']+)["']`)
	reRedocInit     = regexp.MustCompile(`(?s)Redoc\.init\(\s*["']([^"']+)["']`)
	reURLs          = regexp.MustCompile(`(?s)\burls\s*:\s*\[(.*?)\]`)
	reObject        = regexp.MustCompile(`(?s)\{[^{}]*\}`)
	reURLField      = regexp.MustCompile(`\burl\s*:\s*["']([^"']+)["']`)
	reNameField     = regexp.MustCompile(`\bname\s*:\s*["']([^"']+)["']`)
	reSingleURL     = regexp.MustCompile(`\burl\s*:\s*["']([^"']+)["']`)
	reConfigURL     = regexp.MustCompile(`\bconfigUrl\s*:\s*["']([^"']+)["']`)
	reScriptSrc     = regexp.MustCompile(`(?i)<script[^>]*\ssrc\s*=\s*["']([^"']+)["']`)
	reURLVar        = regexp.MustCompile(`\burl\s*:\s*([A-Za-z_$][\w$]*)\s*[,}\n]`)
	reSpecLike      = regexp.MustCompile("(?i)(?:https?://[^\\s\"'`,;=<>()]+|/[^\\s\"'`,;=<>()]*)(?:openapi|swagger|api-docs)[^\\s\"'`,;=<>()]*")
	reInterestingJS = regexp.MustCompile(`(?i)(swagger|initializer|redoc|api-?docs|config)[^/]*\.js(\?|$)`)
)

// Heuristic locations tried when the page declares nothing usable, relative to the
// page directory first, then to the host root.
var heuristics = []string{
	"openapi.json", "openapi.yaml", "swagger.json", "swagger.yaml", "v3/api-docs", "api-docs",
	"/openapi.json", "/openapi.yaml", "/swagger.json", "/v3/api-docs", "/v2/api-docs",
	"/swagger/v1/swagger.json", "/api-docs", "/openapi",
}

const maxCandidates = 16

// Options tune the discovery.
type Options struct {
	// SpecName selects one entry when the page declares several specs (Swagger-UI "urls").
	SpecName string
	// SkipHeuristics disables probing usual locations (/openapi.json, /v3/api-docs…),
	// e.g. when a previous discovery already found nothing: stay polite with partners.
	SkipHeuristics bool
}

// Discover locates the specification behind pageURL whose HTML is page.
func Discover(ctx context.Context, g Getter, pageURL string, page []byte, opt Options) (*Result, error) {
	specName := opt.SpecName
	base, err := url.Parse(pageURL)
	if err != nil {
		return nil, err
	}
	var cands []candidate
	var alternatives []string

	// ?url=… on the page itself (Swagger-UI query configuration)
	if u := base.Query().Get("url"); u != "" {
		cands = append(cands, candidate{url: u, method: "paramètre ?url= de la page"})
	}
	var weak []candidate // spec-looking URLs merely mentioned in the code: tried after explicit configuration
	found, alts, w := parseConfig(string(page), "page HTML")
	cands, alternatives, weak = append(cands, found...), append(alternatives, alts...), append(weak, w...)

	// scripts referenced by the page (swagger-initializer.js, redoc config…)
	for _, m := range reScriptSrc.FindAllStringSubmatch(string(page), -1) {
		if !reInterestingJS.MatchString(m[1]) {
			continue
		}
		jsURL := resolve(base, m[1])
		if u, err := url.Parse(jsURL); err != nil || u.Host != base.Host {
			continue // third-party scripts (CDN bundles) are libraries, not configuration
		}
		resp, err := g.Get(ctx, fetch.Request{URL: jsURL})
		if err != nil {
			continue
		}
		found, alts, w := parseConfig(string(resp.Body), lastSegment(jsURL))
		cands, alternatives, weak = append(cands, found...), append(alternatives, alts...), append(weak, w...)
	}

	// springdoc / Swagger-UI configUrl → JSON {url, urls}
	var expanded []candidate
	for _, c := range cands {
		if c.method != "configUrl" {
			expanded = append(expanded, c)
			continue
		}
		resp, err := g.Get(ctx, fetch.Request{URL: resolve(base, c.url)})
		if err != nil {
			continue
		}
		var conf struct {
			URL  string                       `json:"url"`
			URLs []struct{ URL, Name string } `json:"urls"`
		}
		if json.Unmarshal(resp.Body, &conf) != nil {
			continue
		}
		if conf.URL != "" {
			expanded = append(expanded, candidate{url: conf.URL, method: "swagger-config (" + c.url + ")"})
		}
		for _, u := range conf.URLs {
			expanded = append(expanded, candidate{url: u.URL, name: u.Name, method: "swagger-config (" + c.url + ")"})
			alternatives = append(alternatives, u.Name)
		}
	}
	cands = expanded

	if specName != "" {
		var named []candidate
		for _, c := range cands {
			if c.name == specName {
				named = append(named, c)
			}
		}
		if len(named) == 0 {
			return nil, fmt.Errorf("source.spec_name %q introuvable parmi les specs de la page (%s)", specName, strings.Join(dedupe(alternatives), ", "))
		}
		cands = named
	}

	if specName == "" {
		// same-host mentions first: a page usually documents its own host's API
		var same, other []candidate
		for _, c := range weak {
			if u, err := url.Parse(resolve(base, c.url)); err == nil && u.Host == base.Host {
				same = append(same, c)
			} else {
				other = append(other, c)
			}
		}
		cands = append(append(cands, same...), other...)
	}
	if res := try(ctx, g, base, cands); res != nil {
		res.Alternatives = dedupe(alternatives)
		return res, nil
	}
	if specName == "" && !opt.SkipHeuristics {
		var h []candidate
		for _, p := range heuristics {
			h = append(h, candidate{url: p, method: "emplacement usuel " + p})
		}
		if res := try(ctx, g, base, h); res != nil {
			return res, nil
		}
	}
	return nil, ErrNotFound
}

// parseConfig extracts spec locations from HTML or JavaScript (Swagger-UI / Redoc init
// code). weak lists URLs that merely look like specs, for when nothing explicit works.
func parseConfig(text, origin string) (cands []candidate, names []string, weak []candidate) {
	for _, re := range []*regexp.Regexp{reRedocAttr, reRedocInit} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			cands = append(cands, candidate{url: m[1], method: "Redoc (" + origin + ")"})
		}
	}
	for _, m := range reURLs.FindAllStringSubmatch(text, -1) {
		for _, obj := range reObject.FindAllString(m[1], -1) {
			u := reURLField.FindStringSubmatch(obj)
			if u == nil {
				continue
			}
			c := candidate{url: u[1], method: "Swagger-UI urls (" + origin + ")"}
			if n := reNameField.FindStringSubmatch(obj); n != nil {
				c.name = n[1]
				names = append(names, n[1])
			}
			cands = append(cands, c)
		}
	}
	for _, m := range reConfigURL.FindAllStringSubmatch(text, -1) {
		cands = append(cands, candidate{url: m[1], method: "configUrl"})
	}
	// single "url:" (outside an urls list, which was handled above)
	stripped := reURLs.ReplaceAllString(text, "")
	for _, m := range reSingleURL.FindAllStringSubmatch(stripped, -1) {
		if looksLikeSpec(m[1]) {
			cands = append(cands, candidate{url: m[1], method: "Swagger-UI url (" + origin + ")"})
		}
	}
	// url: someVariable → const someVariable = "…"
	for _, m := range reURLVar.FindAllStringSubmatch(stripped, -1) {
		re := regexp.MustCompile(`(?:const|let|var)\s+` + regexp.QuoteMeta(m[1]) + `\s*=\s*["']([^"']+)["']`)
		if d := re.FindStringSubmatch(text); d != nil {
			cands = append(cands, candidate{url: d[1], method: "Swagger-UI url via " + m[1] + " (" + origin + ")"})
		}
	}
	for _, u := range reSpecLike.FindAllString(text, -1) {
		if looksLikeSpec(u) && !strings.Contains(u, "validator") {
			weak = append(weak, candidate{url: u, method: "URL repérée dans " + origin})
		}
	}
	return cands, names, weak
}

// try fetches candidates in order and returns the first one that is a valid spec.
func try(ctx context.Context, g Getter, base *url.URL, cands []candidate) *Result {
	seen := map[string]bool{}
	n := 0
	for _, c := range cands {
		u := resolve(base, c.url)
		if u == "" || seen[u] || n >= maxCandidates {
			continue
		}
		seen[u] = true
		n++
		resp, err := g.Get(ctx, fetch.Request{URL: u})
		if err != nil || !isSpec(resp.Body) {
			continue
		}
		return &Result{SpecURL: u, Response: resp, Method: c.method}
	}
	return nil
}

func isSpec(b []byte) bool { return normalize.IsSpec(b) }

// looksLikeSpec filters "url:" values that are obviously not spec locations
// (e.g. oauth2RedirectUrl is another key; logos, CSS…).
func looksLikeSpec(u string) bool {
	l := strings.ToLower(u)
	for _, ext := range []string{".png", ".svg", ".css", ".ico", ".html", ".js"} {
		if strings.HasSuffix(l, ext) {
			return false
		}
	}
	return true
}

func resolve(base *url.URL, ref string) string {
	r, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return ""
	}
	u := base.ResolveReference(r)
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.String()
}

func lastSegment(u string) string {
	if i := strings.LastIndex(strings.SplitN(u, "?", 2)[0], "/"); i >= 0 {
		return strings.SplitN(u, "?", 2)[0][i+1:]
	}
	return u
}

func dedupe(list []string) []string {
	var out []string
	for _, s := range list {
		if s != "" && !contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
