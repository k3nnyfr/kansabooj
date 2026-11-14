package diff

import (
	"fmt"
	"strings"
	"testing"

	"github.com/oasdiff/oasdiff/checker"
)

// Every oasdiff rule has a French message, and each translation uses exactly the
// arguments of the English one (same count and types, any order).
func TestFrenchCatalogue(t *testing.T) {
	for _, r := range checker.GetAllRules() {
		if _, ok := frMessages[r.Id]; !ok {
			t.Errorf("règle oasdiff %q non traduite dans locale/fr.yaml", r.Id)
		}
	}
	for key, fr := range frMessages {
		en := enLocalizer(key)                       // without arguments: one "%!x(MISSING)" per verb
		if en == key && key != "at" && key != "in" { // "at"/"in" translate to themselves in English
			t.Errorf("%q : clé inconnue d'oasdiff (supprimée par une nouvelle version ?)", key)
			continue
		}
		var args []any
		for _, verb := range strings.Split(en, "%!")[1:] {
			if strings.HasPrefix(verb, "d(") {
				args = append(args, 1000+len(args))
			} else {
				args = append(args, fmt.Sprintf("‹%d›", len(args)))
			}
		}
		out := fmt.Sprintf(fr, args...)
		if strings.Contains(out, "%!") {
			t.Errorf("%q : arguments incohérents avec l'anglais (%d attendus) : %s", key, len(args), out)
			continue
		}
		for _, a := range args {
			if !strings.Contains(out, fmt.Sprint(a)) {
				t.Errorf("%q : argument %v absent de la traduction : %s", key, a, out)
			}
		}
	}
}

func TestFrenchTextOnRealDiff(t *testing.T) {
	s, err := Compare(mustLoad(t, before).Doc, mustLoad(t, after).Doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range s.Items {
		t.Logf("%s\n   en: %s\n   fr: %s", it.RuleID, it.Text, it.TextFR)
		if it.TextFR == "" || it.Text == "" {
			t.Errorf("%s : texte manquant (en=%q fr=%q)", it.RuleID, it.Text, it.TextFR)
		}
		if it.In("fr") != it.TextFR || it.In("en") != it.Text {
			t.Errorf("%s : In() incohérent", it.RuleID)
		}
	}
}
