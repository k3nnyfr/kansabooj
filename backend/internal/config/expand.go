package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ${VAR}, ${VAR:-default}, ${file:/path/to/secret}
var refPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// expandNode resolves references in every scalar value of the tree (not in keys,
// not in comments). Expansion happens after parsing, so secrets containing YAML
// special characters cannot break the document.
func expandNode(n *yaml.Node, problems *[]string) {
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			expandNode(c, problems)
		}
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			expandNode(n.Content[i], problems)
		}
	case yaml.ScalarNode:
		if strings.Contains(n.Value, "${") {
			n.Value = expandString(n.Value, n.Line, problems)
			n.Tag = "!!str"
			n.Style = 0
		}
	}
}

func expandString(s string, line int, problems *[]string) string {
	return refPattern.ReplaceAllStringFunc(s, func(m string) string {
		ref := m[2 : len(m)-1]
		if path, ok := strings.CutPrefix(ref, "file:"); ok {
			b, err := os.ReadFile(path)
			if err != nil {
				*problems = append(*problems, fmt.Sprintf("ligne %d : fichier secret %q illisible : %v", line, path, err))
				return ""
			}
			return strings.TrimRight(string(b), "\r\n")
		}
		name, def, hasDef := strings.Cut(ref, ":-")
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		if hasDef {
			return def
		}
		*problems = append(*problems, fmt.Sprintf("ligne %d : variable d'environnement %s non définie (utiliser ${%s:-} si elle est optionnelle)", line, name, name))
		return ""
	})
}
