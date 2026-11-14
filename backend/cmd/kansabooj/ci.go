package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/diff"
	"git.k3nny.fr/kansabooj/internal/normalize"
	"git.k3nny.fr/kansabooj/internal/token"
)

// stringList is a repeatable flag.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ", ") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

func runToken(args []string) int {
	if len(args) < 2 || args[0] != "new" {
		fmt.Fprintln(os.Stderr, "usage : kansabooj token new <nom> [--scopes read,validate]")
		return 2
	}
	name := args[1]
	fs := flag.NewFlagSet("token new", flag.ExitOnError)
	scopes := fs.String("scopes", "read", "scopes séparés par des virgules ("+strings.Join(config.Scopes, ", ")+")")
	fs.Parse(args[2:])
	list := strings.Split(*scopes, ",")
	for _, sc := range list {
		if !contains(config.Scopes, sc) {
			fmt.Fprintf(os.Stderr, "scope %q inconnu (%s)\n", sc, strings.Join(config.Scopes, ", "))
			return 2
		}
	}
	tok, hash := token.New()
	fmt.Printf(`Jeton à conserver dans le coffre de secrets de l'appelant (affiché une seule fois) :

  %s

À ajouter dans la configuration (server.tokens) — seul le hash y figure :

  - name: %s
    hash: %s
    scopes: [%s]
`, tok, name, hash, strings.Join(list, ", "))
	return 0
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// runDiff compares two specs and fails (exit 1) on changes at or above --fail-on
// within the scope. A side written "kansabooj:<api-id>" is the version currently
// recorded by a Kansabooj server, whose exposed operations and ignore rules are
// then applied to both sides.
func runDiff(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	failOn := fs.String("fail-on", "error", "niveau qui fait échouer : error | warn | info | none")
	format := fs.String("format", "text", "text | json")
	server := fs.String("server", os.Getenv("KANSABOOJ_SERVER"), "URL du serveur Kansabooj (env KANSABOOJ_SERVER)")
	tok := fs.String("token", os.Getenv("KANSABOOJ_TOKEN"), "jeton d'API, scope read (env KANSABOOJ_TOKEN)")
	var exposed, ignore stringList
	fs.Var(&exposed, "exposed", "opération réexposée, répétable (ex : \"GET /orders/{id}\")")
	fs.Var(&ignore, "ignore", "chemin ignoré, répétable (ex : $.servers)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `usage : kansabooj diff [options] <base> <révision>

<base> et <révision> : fichier, URL http(s), ou kansabooj:<api-id> (version enregistrée sur le serveur).
Code retour : 0 = rien au-dessus de --fail-on, 1 = changements bloquants, 2 = erreur.

Exemple (CI d'une équipe productrice) :
  kansabooj diff --server https://kansabooj.interne kansabooj:referentiel-clients build/openapi.yaml

`)
		fs.PrintDefaults()
	}
	fs.Parse(args)
	if fs.NArg() != 2 || !contains([]string{"error", "warn", "info", "none"}, *failOn) {
		fs.Usage()
		return 2
	}

	cl := &kbClient{server: strings.TrimRight(*server, "/"), token: *tok, http: &http.Client{Timeout: 60 * time.Second}}
	var docs [2][]byte
	for i, ref := range fs.Args() {
		b, apiCfg, err := cl.read(ctx, ref)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s : %v\n", ref, err)
			return 2
		}
		docs[i] = b
		if apiCfg != nil { // scope and anti-noise rules of the monitored API
			if len(exposed) == 0 {
				exposed = apiCfg.Gateway.ExposedOperations
			}
			ignore = append(ignore, apiCfg.Ignore...)
		}
	}
	var specs [2]*normalize.Result
	for i := range docs {
		// multi-file specs: external $refs resolved relative to each side (an http(s) URL,
		// or a local file whose directory bounds the files that may be read)
		o := normalize.Options{Ignore: ignore, Ctx: ctx}
		switch ref := fs.Arg(i); {
		case strings.HasPrefix(ref, "kansabooj:"): // stored normalized form, already self-contained
		case strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://"):
			o.Location = ref
			o.Fetch = func(ctx context.Context, u string) ([]byte, error) { return cl.fetch(ctx, u, false) }
		default:
			if abs, err := filepath.Abs(ref); err == nil {
				o.Location = (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
				o.LocalDir = filepath.Dir(abs)
			}
		}
		r, err := normalize.NormalizeWith(docs[i], o)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s : %v\n", fs.Arg(i), err)
			return 2
		}
		specs[i] = r
	}

	var sum *diff.Summary
	if specs[0].SHA256 == specs[1].SHA256 {
		sum = &diff.Summary{VersionFrom: specs[0].Version, VersionTo: specs[1].Version}
	} else {
		var err error
		if sum, err = diff.Compare(specs[0].Doc, specs[1].Doc, exposed); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}

	blocking := 0
	for _, it := range sum.Items {
		if it.InScope && *failOn != "none" && diff.LevelAtLeast(it.Level, *failOn) && it.RuleID != diff.RuleVersionChanged && it.RuleID != diff.RuleNonSemantic {
			blocking++
		}
	}

	if *format == "json" {
		json.NewEncoder(os.Stdout).Encode(map[string]any{
			"identical": specs[0].SHA256 == specs[1].SHA256, "version_from": sum.VersionFrom, "version_to": sum.VersionTo,
			"errors": sum.Errors, "warns": sum.Warns, "infos": sum.Infos, "in_scope": sum.InScope,
			"blocking": blocking, "fail_on": *failOn, "items": sum.Items,
		})
	} else {
		if len(sum.Items) == 0 {
			fmt.Println("✔ contrats identiques")
		} else {
			fmt.Printf("%d cassant(s), %d à surveiller, %d info — %d dans le périmètre\n", sum.Errors, sum.Warns, sum.Infos, sum.InScope)
			for _, it := range sum.Items {
				fmt.Println("  " + formatItem(it))
			}
		}
		if blocking > 0 {
			fmt.Printf("✘ %d changement(s) bloquant(s) (niveau ≥ %s dans le périmètre)\n", blocking, *failOn)
		}
	}
	if blocking > 0 {
		return 1
	}
	return 0
}

type kbClient struct {
	server, token string
	http          *http.Client
}

// read returns the content designated by ref and, for kansabooj:<id>, the API configuration.
func (c *kbClient) read(ctx context.Context, ref string) ([]byte, *config.API, error) {
	if id, ok := strings.CutPrefix(ref, "kansabooj:"); ok {
		if c.server == "" {
			return nil, nil, errors.New("--server (ou KANSABOOJ_SERVER) requis pour kansabooj:<id>")
		}
		var detail struct {
			API struct {
				SnapshotID *int64 `json:"snapshot_id"`
			} `json:"api"`
			Config config.API `json:"config"`
		}
		if err := c.getJSON(ctx, "/api/apis/"+url.PathEscape(id), &detail); err != nil {
			return nil, nil, err
		}
		if detail.API.SnapshotID == nil {
			return nil, nil, errors.New("aucune version enregistrée pour cette API")
		}
		b, err := c.get(ctx, fmt.Sprintf("/api/snapshots/%d/spec?form=normalized", *detail.API.SnapshotID))
		return b, &detail.Config, err
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		b, err := c.fetch(ctx, ref, false)
		return b, nil, err
	}
	b, err := os.ReadFile(ref)
	return b, nil, err
}

func (c *kbClient) get(ctx context.Context, path string) ([]byte, error) {
	return c.fetch(ctx, c.server+path, true)
}

func (c *kbClient) getJSON(ctx context.Context, path string, out any) error {
	b, err := c.get(ctx, path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func (c *kbClient) fetch(ctx context.Context, u string, auth bool) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if auth && c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(b, &e)
		return nil, fmt.Errorf("HTTP %d %s", resp.StatusCode, e.Error)
	}
	return b, err
}
