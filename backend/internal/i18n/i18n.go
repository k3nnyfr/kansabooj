// Package i18n holds the server-side message catalog (notifications). The UI has
// its own catalog in frontend/src/i18n.ts; keep the wording consistent.
package i18n

import "fmt"

var catalog = map[string]map[string]string{
	"fr": {
		"change.title":      "%s : %s",
		"change.breaking":   "%d changement(s) cassant(s)",
		"change.warn":       "%d changement(s) à surveiller",
		"change.info":       "%d changement(s) informatif(s)",
		"change.version":    "Version %s → %s",
		"change.noversion":  "Version inchangée (%s)",
		"change.more":       "… et %d autre(s)",
		"change.outofscope": "%d changement(s) hors périmètre non listé(s)",
		"stale.title":       "%s : source injoignable",
		"stale.body":        "%d échecs consécutifs. Dernière erreur : %s",
		"recovered.title":   "%s : source de nouveau accessible",
		"recovered.body":    "Les vérifications fonctionnent à nouveau.",
		"button.view":       "Voir l'évolution",
		"button.api":        "Voir l'API",
		"level.error":       "CASSANT",
		"level.warn":        "ATTENTION",
		"level.info":        "INFO",
		"owner":             "Équipe : %s",
		"digest.title":      "Kansabooj : %d évolution(s) sur %d API(s)",
		"digest.since":      "Récapitulatif depuis le %s",
		"digest.line":       "[%s] %s — %s : %s",
		"digest.more":       "… et %d autre(s) évolution(s)",
		"button.open":       "Ouvrir Kansabooj",
	},
	"en": {
		"change.title":      "%s: %s",
		"change.breaking":   "%d breaking change(s)",
		"change.warn":       "%d change(s) to review",
		"change.info":       "%d informational change(s)",
		"change.version":    "Version %s → %s",
		"change.noversion":  "Version unchanged (%s)",
		"change.more":       "… and %d more",
		"change.outofscope": "%d out-of-scope change(s) not listed",
		"stale.title":       "%s: source unreachable",
		"stale.body":        "%d consecutive failures. Last error: %s",
		"recovered.title":   "%s: source reachable again",
		"recovered.body":    "Checks are working again.",
		"button.view":       "View change",
		"button.api":        "View API",
		"level.error":       "BREAKING",
		"level.warn":        "WARNING",
		"level.info":        "INFO",
		"owner":             "Team: %s",
		"digest.title":      "Kansabooj: %d change(s) across %d API(s)",
		"digest.since":      "Digest since %s",
		"digest.line":       "[%s] %s — %s: %s",
		"digest.more":       "… and %d more change(s)",
		"button.open":       "Open Kansabooj",
	},
}

// T returns the message for key in lang (falling back to French, then to the key).
func T(lang, key string, args ...any) string {
	msg, ok := catalog[lang][key]
	if !ok {
		if msg, ok = catalog["fr"][key]; !ok {
			return key
		}
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}
