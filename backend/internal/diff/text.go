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
	s.Items = append(s.Items, item(RuleHTMLChanged, LevelWarn, true,
		fmt.Sprintf("documentation changed (degraded mode, no contract analysis): +%d / −%d line(s), check manually", len(added), len(removed)),
		fmt.Sprintf("documentation modifiée (mode dégradé, sans analyse du contrat) : +%d / −%d ligne(s) — vérification manuelle nécessaire", len(added), len(removed))))
	for i, l := range removed {
		if i == maxTextLines {
			break
		}
		t := "− " + truncate(l, 200)
		s.Items = append(s.Items, item(RuleHTMLLineRemoved, LevelInfo, true, t, t))
	}
	for i, l := range added {
		if i == maxTextLines {
			break
		}
		t := "+ " + truncate(l, 200)
		s.Items = append(s.Items, item(RuleHTMLLineAdded, LevelInfo, true, t, t))
	}
	s.Warns, s.Infos, s.InScope = 1, len(s.Items)-1, len(s.Items)
	return s
}

// FirstCaptureText describes the first degraded-mode snapshot.
func FirstCaptureText(lines int) *Summary {
	return &Summary{Infos: 1, InScope: 1, Items: []Item{item(RuleFirstCapture, LevelInfo, true,
		fmt.Sprintf("first capture of the documentation (degraded mode: page text, %d lines)", lines),
		fmt.Sprintf("première capture de la documentation (mode dégradé : texte de la page, %d lignes)", lines))}}
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
	return &Summary{VersionTo: version, Warns: 1, InScope: 1, Items: []Item{item("capture-mode-changed", LevelWarn, true,
		fmt.Sprintf("capture mode changed: %s → %s (no semantic comparison with the previous version)", from, to),
		fmt.Sprintf("mode de capture modifié : %s → %s (comparaison sémantique impossible avec la version précédente)", from, to))}}
}
