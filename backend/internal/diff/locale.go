package diff

import (
	_ "embed"
	"fmt"

	"github.com/oasdiff/oasdiff/checker"
	"gopkg.in/yaml.v3"
)

// oasdiff only ships English (and a few other languages): locale/fr.yaml translates its
// messages by rule id, with the same arguments (possibly reordered with %[n]s).
//
//go:embed locale/fr.yaml
var frYAML []byte

var frMessages = func() map[string]string {
	m := map[string]string{}
	if err := yaml.Unmarshal(frYAML, &m); err != nil {
		panic(fmt.Sprintf("diff/locale/fr.yaml : %v", err))
	}
	return m
}()

var enLocalizer = checker.NewDefaultLocalizer()

// frLocalizer renders oasdiff messages in French, falling back to English for a rule
// added by a newer oasdiff and not translated yet (TestFrenchCatalogue catches it).
func frLocalizer(key string, args ...any) string {
	if p, ok := frMessages[key]; ok {
		return fmt.Sprintf(p, args...)
	}
	return enLocalizer(key, args...)
}

// In returns the item text in lang ("fr" or "en"); items stored before v0.6 only
// have their original text.
func (it Item) In(lang string) string {
	if lang == "fr" && it.TextFR != "" {
		return it.TextFR
	}
	return it.Text
}
