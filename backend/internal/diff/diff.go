// Package diff wraps oasdiff: it compares two canonical OpenAPI 3 documents and
// returns classified changes, flagged in/out of the scope re-exposed in the gateway.
package diff

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/oasdiff/oasdiff/checker"
	odiff "github.com/oasdiff/oasdiff/diff"
	"github.com/oasdiff/oasdiff/load"
)

const (
	LevelError = "error" // breaking
	LevelWarn  = "warn"  // potentially breaking
	LevelInfo  = "info"

	RuleVersionChanged = "api-version-changed"
	RuleNonSemantic    = "non-semantic-change"
	RuleFirstCapture   = "first-capture"
)

type Item struct {
	RuleID  string `json:"rule_id"`
	Level   string `json:"level"`
	Method  string `json:"method,omitempty"`
	Path    string `json:"path,omitempty"`
	Text    string `json:"text"`              // English (oasdiff's own wording)
	TextFR  string `json:"text_fr,omitempty"` // French (locale/fr.yaml)
	InScope bool   `json:"in_scope"`
}

// item builds a Kansabooj-generated item (not from oasdiff) in both languages.
func item(rule, level string, inScope bool, en, fr string) Item {
	return Item{RuleID: rule, Level: level, InScope: inScope, Text: en, TextFR: fr}
}

type Summary struct {
	VersionFrom string `json:"version_from,omitempty"`
	VersionTo   string `json:"version_to,omitempty"`
	Items       []Item `json:"items"`
	Errors      int    `json:"errors"`
	Warns       int    `json:"warns"`
	Infos       int    `json:"infos"`
	InScope     int    `json:"in_scope"`
}

// MaxLevel returns the most severe level present, or "" when there is no item.
func (s *Summary) MaxLevel() string {
	switch {
	case s.Errors > 0:
		return LevelError
	case s.Warns > 0:
		return LevelWarn
	case s.Infos > 0:
		return LevelInfo
	}
	return ""
}

// Compare diffs prev → cur. It is only called when the canonical hashes differ,
// so it always returns at least one item.
func Compare(prev, cur *openapi3.T, exposed []string) (*Summary, error) {
	s := &Summary{VersionFrom: version(prev), VersionTo: version(cur)}
	scope := newScope(exposed)

	base := &load.SpecInfo{Url: "previous", Spec: prev, Version: s.VersionFrom}
	rev := &load.SpecInfo{Url: "current", Spec: cur, Version: s.VersionTo}
	d, sources, err := odiff.GetWithOperationsSourcesMap(odiff.NewConfig(), base, rev)
	if err != nil {
		return nil, fmt.Errorf("calcul du diff : %w", err)
	}
	changes := checker.CheckBackwardCompatibilityUntilLevel(checker.NewConfig(checker.GetAllChecks()), d, sources, checker.INFO)
	loc := checker.NewDefaultLocalizer()
	for _, c := range changes {
		it := Item{
			RuleID: c.GetId(),
			Level:  level(c.GetLevel()),
			Method: c.GetOperation(),
			Path:   c.GetPath(),
			Text:   c.GetUncolorizedText(loc),
			TextFR: c.GetUncolorizedText(frLocalizer),
		}
		it.InScope = scope.contains(it.Method, it.Path)
		s.Items = append(s.Items, it)
	}

	if s.VersionFrom != s.VersionTo {
		from, to := orNA(s.VersionFrom), orNA(s.VersionTo)
		s.Items = append(s.Items, item(RuleVersionChanged, LevelInfo, true,
			fmt.Sprintf("API version changed: %s → %s", from, to), fmt.Sprintf("version de l'API modifiée : %s → %s", from, to)))
	}
	if len(s.Items) == 0 {
		s.Items = append(s.Items, item(RuleNonSemantic, LevelInfo, len(exposed) == 0,
			"changes without contract impact (descriptions, examples, metadata…)",
			"modifications sans impact sur le contrat (descriptions, exemples, métadonnées…)"))
	}

	sort.SliceStable(s.Items, func(i, j int) bool {
		if r := rank(s.Items[i].Level) - rank(s.Items[j].Level); r != 0 {
			return r < 0
		}
		if s.Items[i].Path != s.Items[j].Path {
			return s.Items[i].Path < s.Items[j].Path
		}
		return s.Items[i].Method < s.Items[j].Method
	})
	for _, it := range s.Items {
		switch it.Level {
		case LevelError:
			s.Errors++
		case LevelWarn:
			s.Warns++
		default:
			s.Infos++
		}
		if it.InScope {
			s.InScope++
		}
	}
	return s, nil
}

// FirstCapture describes the very first snapshot of an API.
func FirstCapture(cur *openapi3.T, operations int) *Summary {
	v := orNA(version(cur))
	return &Summary{VersionTo: version(cur), Infos: 1, InScope: 1, Items: []Item{item(RuleFirstCapture, LevelInfo, true,
		fmt.Sprintf("first capture of the contract (version %s, %d operations)", v, operations),
		fmt.Sprintf("première capture du contrat (version %s, %d opérations)", v, operations))}}
}

func level(l checker.Level) string {
	switch l {
	case checker.ERR:
		return LevelError
	case checker.WARN:
		return LevelWarn
	}
	return LevelInfo
}

func rank(l string) int {
	switch l {
	case LevelError:
		return 0
	case LevelWarn:
		return 1
	}
	return 2
}

// LevelAtLeast reports whether l is as severe as min.
func LevelAtLeast(l, min string) bool { return rank(l) <= rank(min) }

func version(t *openapi3.T) string {
	if t == nil || t.Info == nil {
		return ""
	}
	return t.Info.Version
}

func orNA(v string) string {
	if v == "" {
		return "n/a"
	}
	return v
}

// scope matches "METHOD /path" entries; path parameter names are ignored
// ({id} == {orderId}) and a trailing * matches any suffix.
type scope struct {
	all      bool
	exact    map[string]bool
	prefixes []string
}

var paramRe = regexp.MustCompile(`\{[^}]*\}`)

func key(method, path string) string {
	return strings.ToUpper(method) + " " + paramRe.ReplaceAllString(path, "{}")
}

func newScope(exposed []string) *scope {
	s := &scope{all: len(exposed) == 0, exact: map[string]bool{}}
	for _, e := range exposed {
		method, path, _ := strings.Cut(e, " ")
		if p, ok := strings.CutSuffix(path, "*"); ok {
			s.prefixes = append(s.prefixes, key(method, p))
		} else {
			s.exact[key(method, path)] = true
		}
	}
	return s
}

func (s *scope) contains(method, path string) bool {
	if s.all {
		return true
	}
	if path == "" {
		return false // component-level change not attached to an operation
	}
	k := key(method, path)
	if s.exact[k] {
		return true
	}
	for _, p := range s.prefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}
