package normalize

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// refReader reads the targets of external $refs, the only way a spec makes Kansabooj
// read another document. SECURITY: http(s) only through the API's fetch client (deny/
// allow lists, origin-bound credentials), file:// only under Options.LocalDir, bounded.
type refReader struct {
	o       Options
	ctx     context.Context
	fetched int
	cache   map[string][]byte
}

func newRefReader(o Options) (*refReader, *url.URL, error) {
	if o.Location == "" || (o.Fetch == nil && o.LocalDir == "") {
		return nil, nil, errors.New("la spec contient des $ref externes : leur résolution nécessite l'URL d'origine")
	}
	loc, err := url.Parse(o.Location)
	if err != nil {
		return nil, nil, err
	}
	ctx := o.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return &refReader{o: o, ctx: ctx, cache: map[string][]byte{}}, loc, nil
}

func (r *refReader) read(u *url.URL) ([]byte, error) {
	key := u.Scheme + "://" + u.Host + u.Path
	if b, ok := r.cache[key]; ok {
		return b, nil
	}
	if r.fetched++; r.fetched > maxExternalRefs {
		return nil, fmt.Errorf("plus de %d fichiers référencés : abandon", maxExternalRefs)
	}
	var b []byte
	var err error
	switch {
	case (u.Scheme == "http" || u.Scheme == "https") && r.o.Fetch != nil:
		b, err = r.o.Fetch(r.ctx, (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path, RawQuery: u.RawQuery}).String())
	case u.Scheme == "file" && r.o.LocalDir != "":
		b, err = readLocal(r.o.LocalDir, u.Path)
	default:
		return nil, fmt.Errorf("$ref externe %q refusée : seules les URLs http(s) sont autorisées", u)
	}
	if err != nil {
		return nil, err
	}
	r.cache[key] = b
	return b, nil
}

// readLocal reads p only if it lies under dir once symlinks are resolved.
func readLocal(dir, p string) ([]byte, error) {
	base, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(filepath.FromSlash(p))
	if err != nil {
		return nil, fmt.Errorf("$ref externe %s : %w", p, err)
	}
	if rel, err := filepath.Rel(base, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("$ref externe %s refusée : hors du dossier de la spec (%s)", p, dir)
	}
	return os.ReadFile(real)
}

// inlineRefs replaces the external $refs of a JSON tree by the content they point to,
// recursively (refs inside a referenced document are relative to that document). A
// reference that loops back to a document being inlined is hoisted to #/definitions
// (Swagger 2.0 schemas), the only place where recursion is meaningful.
func inlineRefs(root map[string]any, o Options) (map[string]any, error) {
	r, loc, err := newRefReader(o)
	if err != nil {
		return nil, err
	}
	in := &inliner{r: r, root: root, rootURL: withoutFragment(loc), docs: map[string]any{}, hoisted: map[string]string{}}
	in.docs[in.rootURL.String()] = root
	out, err := in.walk(root, in.rootURL, nil)
	if err != nil {
		return nil, err
	}
	return out.(map[string]any), nil
}

type inliner struct {
	r       *refReader
	root    map[string]any
	rootURL *url.URL
	docs    map[string]any    // parsed documents by URL (without fragment)
	hoisted map[string]string // absolute ref → name under #/definitions
}

func (in *inliner) walk(n any, doc *url.URL, stack []string) (any, error) {
	switch t := n.(type) {
	case map[string]any:
		if ref, ok := t["$ref"].(string); ok {
			return in.ref(ref, doc, stack)
		}
		out := make(map[string]any, len(t))
		for k, v := range t {
			w, err := in.walk(v, doc, stack)
			if err != nil {
				return nil, err
			}
			out[k] = w
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, v := range t {
			w, err := in.walk(v, doc, stack)
			if err != nil {
				return nil, err
			}
			out[i] = w
		}
		return out, nil
	}
	return n, nil
}

func (in *inliner) ref(ref string, doc *url.URL, stack []string) (any, error) {
	rel, err := url.Parse(ref)
	if err != nil {
		return nil, fmt.Errorf("$ref %q invalide : %w", ref, err)
	}
	target := doc.ResolveReference(rel)
	file := withoutFragment(target)
	if file.String() == in.rootURL.String() {
		return map[string]any{"$ref": "#" + target.Fragment}, nil // the root document's own definitions stay as they are
	}
	abs := target.String()
	if name, ok := in.hoisted[abs]; ok {
		return map[string]any{"$ref": "#/definitions/" + name}, nil
	}
	for _, s := range stack {
		if s == abs { // recursion: hoist the schema once under #/definitions
			name := in.hoistName(target)
			in.hoisted[abs] = name
			content, err := in.resolve(file, target.Fragment)
			if err != nil {
				return nil, err
			}
			defs, _ := in.root["definitions"].(map[string]any)
			if defs == nil {
				defs = map[string]any{}
				in.root["definitions"] = defs
			}
			defs[name] = map[string]any{} // placeholder: further loops reuse the name
			w, err := in.walk(content, file, nil)
			if err != nil {
				return nil, err
			}
			defs[name] = w
			return map[string]any{"$ref": "#/definitions/" + name}, nil
		}
	}
	content, err := in.resolve(file, target.Fragment)
	if err != nil {
		return nil, err
	}
	return in.walk(content, file, append(stack, abs))
}

func (in *inliner) resolve(file *url.URL, fragment string) (any, error) {
	key := file.String()
	d, ok := in.docs[key]
	if !ok {
		b, err := in.r.read(file)
		if err != nil {
			return nil, err
		}
		if d, err = decode([]byte(strings.TrimSpace(string(b)))); err != nil {
			return nil, fmt.Errorf("$ref externe %s : %w", key, err)
		}
		in.docs[key] = d
	}
	return pointer(d, fragment, key)
}

func (in *inliner) hoistName(target *url.URL) string {
	name := path.Base(target.Fragment)
	if name == "" || name == "." || name == "/" {
		name = strings.TrimSuffix(path.Base(target.Path), path.Ext(target.Path))
	}
	defs, _ := in.root["definitions"].(map[string]any)
	base := name
	for i := 2; defs[name] != nil; i++ {
		name = base + strconv.Itoa(i)
	}
	return name
}

// pointer resolves a JSON pointer fragment ("/definitions/Pet", "" for the document).
func pointer(d any, fragment, where string) (any, error) {
	if fragment == "" || fragment == "/" {
		return d, nil
	}
	cur := d
	for _, tok := range strings.Split(strings.TrimPrefix(fragment, "/"), "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		switch t := cur.(type) {
		case map[string]any:
			v, ok := t[tok]
			if !ok {
				return nil, fmt.Errorf("$ref %s#%s : %q introuvable", where, fragment, tok)
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(t) {
				return nil, fmt.Errorf("$ref %s#%s : index %q invalide", where, fragment, tok)
			}
			cur = t[i]
		default:
			return nil, fmt.Errorf("$ref %s#%s : chemin invalide", where, fragment)
		}
	}
	return cur, nil
}

func withoutFragment(u *url.URL) *url.URL {
	c := *u
	c.Fragment, c.RawFragment = "", ""
	return &c
}
