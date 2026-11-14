package diff

import (
	"fmt"
	"strings"
)

const (
	RuleHTMLChanged     = "html-content-changed"
	RuleHTMLLineAdded   = "html-line-added"
	RuleHTMLLineRemoved = "html-line-removed"

	maxTextLines = 10
)

// CompareText is the degraded-mode diff of two extracted documentation texts: no
// semantic analysis, so the summary is a warning ("check manually") listing the
// first added and removed lines.
func CompareText(prev, cur []byte) *Summary {
	added, removed := lineDelta(splitLines(prev), splitLines(cur))
	s := &Summary{}
	s.Items = append(s.Items, Item{RuleID: RuleHTMLChanged, Level: LevelWarn, InScope: true,
		Text: fmt.Sprintf("documentation modifiée (mode dégradé, sans analyse du contrat) : +%d / −%d ligne(s) — vérification manuelle nécessaire", len(added), len(removed))})
	for i, l := range removed {
		if i == maxTextLines {
			break
		}
		s.Items = append(s.Items, Item{RuleID: RuleHTMLLineRemoved, Level: LevelInfo, InScope: true, Text: "− " + truncate(l, 200)})
	}
	for i, l := range added {
		if i == maxTextLines {
			break
		}
		s.Items = append(s.Items, Item{RuleID: RuleHTMLLineAdded, Level: LevelInfo, InScope: true, Text: "+ " + truncate(l, 200)})
	}
	s.Warns, s.Infos, s.InScope = 1, len(s.Items)-1, len(s.Items)
	return s
}

// FirstCaptureText describes the first degraded-mode snapshot.
func FirstCaptureText(lines int) *Summary {
	return &Summary{Infos: 1, InScope: 1, Items: []Item{{RuleID: RuleFirstCapture, Level: LevelInfo, InScope: true,
		Text: fmt.Sprintf("première capture de la documentation (mode dégradé : texte de la page, %d lignes)", lines)}}}
}

func splitLines(b []byte) []string {
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// lineDelta returns the lines present in cur but not in prev (added) and vice versa,
// as multisets, in document order.
func lineDelta(prev, cur []string) (added, removed []string) {
	count := map[string]int{}
	for _, l := range prev {
		count[l]++
	}
	for _, l := range cur {
		if count[l] > 0 {
			count[l]--
		} else {
			added = append(added, l)
		}
	}
	count = map[string]int{}
	for _, l := range cur {
		count[l]++
	}
	for _, l := range prev {
		if count[l] > 0 {
			count[l]--
		} else {
			removed = append(removed, l)
		}
	}
	return added, removed
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// ModeChanged describes a switch between degraded (html) and semantic capture.
func ModeChanged(from, to, version string) *Summary {
	return &Summary{VersionTo: version, Warns: 1, InScope: 1, Items: []Item{{RuleID: "capture-mode-changed", Level: LevelWarn, InScope: true,
		Text: fmt.Sprintf("mode de capture modifié : %s → %s (comparaison sémantique impossible avec la version précédente)", from, to)}}}
}
