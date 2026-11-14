package normalize

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// FormatHTML marks snapshots taken in degraded mode (documentation without a spec).
const FormatHTML = "html"

// HTMLText is the degraded mode for documentation pages without a machine-readable
// spec: the visible text is extracted (scripts, styles, navigation chrome removed),
// one block per line, and hashed. ignore holds regular expressions whose matches
// are removed first (dates, build numbers, CSRF tokens…).
func HTMLText(page []byte, ignore []string) (*Result, error) {
	res := []*regexp.Regexp{}
	for _, p := range ignore {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("text_ignore %q : %w", p, err)
		}
		res = append(res, re)
	}
	doc, err := html.Parse(bytes.NewReader(page))
	if err != nil {
		return nil, fmt.Errorf("HTML illisible : %w", err)
	}
	var title string
	var lines []string
	var cur strings.Builder
	flush := func() {
		line := strings.Join(strings.Fields(cur.String()), " ")
		for _, re := range res {
			line = strings.TrimSpace(re.ReplaceAllString(line, ""))
		}
		if line != "" {
			lines = append(lines, line)
		}
		cur.Reset()
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.DataAtom {
			case atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Svg, atom.Head, atom.Nav, atom.Footer, atom.Iframe:
				if n.DataAtom == atom.Head {
					if t := find(n, atom.Title); t != nil && t.FirstChild != nil {
						title = strings.TrimSpace(t.FirstChild.Data)
					}
				}
				return
			}
		}
		if n.Type == html.TextNode {
			cur.WriteString(n.Data)
			cur.WriteByte(' ')
		}
		block := n.Type == html.ElementNode && isBlock(n.DataAtom)
		if block {
			flush()
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if block {
			flush()
		}
	}
	walk(doc)
	flush()
	if len(lines) == 0 {
		return nil, fmt.Errorf("%w : aucun texte exploitable dans la page", ErrNotSpec)
	}
	text := []byte(strings.Join(lines, "\n") + "\n")
	sum := sha256.Sum256(text)
	return &Result{Format: FormatHTML, Canonical: text, SHA256: hex.EncodeToString(sum[:]), Title: title}, nil
}

func find(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := find(c, a); f != nil {
			return f
		}
	}
	return nil
}

func isBlock(a atom.Atom) bool {
	switch a {
	case atom.P, atom.Div, atom.Section, atom.Article, atom.Main, atom.Header, atom.Aside,
		atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6, atom.Li, atom.Ul, atom.Ol, atom.Dl, atom.Dt, atom.Dd,
		atom.Table, atom.Tr, atom.Td, atom.Th, atom.Thead, atom.Tbody, atom.Pre, atom.Blockquote, atom.Br, atom.Hr,
		atom.Form, atom.Fieldset, atom.Details, atom.Summary, atom.Figure, atom.Figcaption, atom.Caption:
		return true
	}
	return false
}
