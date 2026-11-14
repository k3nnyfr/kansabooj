// Command kansabooj monitors partner API contracts.
//
//	kansabooj validate [--config apis.yml]
//	kansabooj check    [--config apis.yml] [--db kansabooj.db] [--dry-run] <api-id>... | --all
//	kansabooj history  [--config apis.yml] [--db kansabooj.db] <api-id>
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/diff"
	"git.k3nny.fr/kansabooj/internal/httpapi"
	"git.k3nny.fr/kansabooj/internal/monitor"
	"git.k3nny.fr/kansabooj/internal/scheduler"
	"git.k3nny.fr/kansabooj/internal/store"
)

const usage = `kansabooj — surveillance des contrats d'API partenaires

Commandes :
  validate   valide la configuration (schéma, variables d'environnement, références)
  check      exécute un check sur une ou plusieurs APIs (--all pour toutes, --dry-run pour ne rien enregistrer)
  history    affiche la timeline des évolutions d'une API
  serve      serveur HTTP (API REST + interface) et vérifications périodiques
  migrate    applique les migrations de schéma (compte avec droits DDL, cf. database.migrate: false)
  diff       compare deux specs (fichiers, URL ou kansabooj:<api-id>) — contrôle de contrat en CI
  token      génère un jeton d'API (token new <nom> --scopes read,validate)

Options communes : --config (défaut config/apis.yml), --db (défaut : section database, sinon data/kansabooj.db)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var code int
	switch os.Args[1] {
	case "validate":
		code = runValidate(os.Args[2:])
	case "check":
		code = runCheck(ctx, os.Args[2:])
	case "history":
		code = runHistory(ctx, os.Args[2:])
	case "serve":
		code = runServe(ctx, os.Args[2:])
	case "migrate":
		code = runMigrate(ctx, os.Args[2:])
	case "diff":
		code = runDiff(ctx, os.Args[2:])
	case "token":
		code = runToken(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "commande inconnue %q\n\n%s", os.Args[1], usage)
		code = 2
	}
	os.Exit(code)
}

type common struct {
	fs     *flag.FlagSet
	config string
	db     string
}

func newFlags(name string) *common {
	c := &common{fs: flag.NewFlagSet(name, flag.ExitOnError)}
	c.fs.StringVar(&c.config, "config", env("KANSABOOJ_CONFIG", "config/apis.yml"), "fichier de configuration (env KANSABOOJ_CONFIG)")
	c.fs.StringVar(&c.db, "db", os.Getenv("KANSABOOJ_DB"),
		"base : URL postgres://… (ou \"postgres\" pour les variables PG*), ou chemin SQLite (env KANSABOOJ_DB).\n"+
			"Par défaut : section database de la configuration, sinon data/kansabooj.db")
	return c
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func loadConfig(path string) (*config.Config, bool) {
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return cfg, false
	}
	return cfg, true
}

func runValidate(args []string) int {
	f := newFlags("validate")
	f.fs.Parse(args)
	cfg, ok := loadConfig(f.config)
	if !ok {
		return 1
	}
	fmt.Printf("✔ %s valide : %d API(s), %d groupe(s), %d notifier(s)\n", f.config, len(cfg.APIs), len(cfg.Groups), len(cfg.Notifiers))
	for _, a := range cfg.APIs {
		auth := "sans auth"
		if a.Auth != nil {
			auth = "auth " + a.Auth.Type
		}
		group := a.Group
		if group == "" {
			group = "—"
		}
		fmt.Printf("  - %-24s groupe %-16s réseau %-10s toutes les %-6s %-28s %s\n", a.ID, group, a.Network, a.Interval.String(), auth, a.Source.URL)
		if len(a.Tags) > 0 {
			fmt.Printf("    tags : %s\n", strings.Join(a.Tags, ", "))
		}
	}
	return 0
}

const defaultSQLite = "data/kansabooj.db"

// dbOptions resolves the database: --db / KANSABOOJ_DB, then the `database` section,
// then a local SQLite file. Schema and pool settings of the section always apply.
func dbOptions(flagDB string, cfg *config.Config) (store.Options, string) {
	d := cfg.Database
	o := store.Options{Schema: d.Schema, ApplicationName: d.ApplicationName, MaxOpenConns: d.MaxOpenConns,
		MaxIdleConns: d.MaxIdleConns, ConnMaxLifetime: d.ConnMaxLifetime.Std(), Migrate: d.AutoMigrate()}
	switch {
	case flagDB != "":
		o.DSN = flagDB
	case d.Configured():
		return withDSN(o, d.DSN()), d.Redacted()
	default:
		o.DSN = defaultSQLite
	}
	return o, redactDSN(o.DSN)
}

func withDSN(o store.Options, dsn string) store.Options { o.DSN = dsn; return o }

func redactDSN(dsn string) string {
	if store.DialectOf(dsn) != store.Postgres {
		return "sqlite:" + dsn
	}
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		return u.Redacted()
	}
	return dsn
}

func openStore(ctx context.Context, o store.Options, cfg *config.Config) (*store.Store, error) {
	if dir := filepath.Dir(o.DSN); store.DialectOf(o.DSN) == store.SQLite && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}
	st, err := store.OpenWith(ctx, o)
	if err != nil {
		return nil, err
	}
	if err := st.SyncAPIs(ctx, cfg.APIs); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

func runMigrate(ctx context.Context, args []string) int {
	f := newFlags("migrate")
	f.fs.Parse(args)
	cfg, ok := loadConfig(f.config)
	if !ok {
		return 1
	}
	o, target := dbOptions(f.db, cfg)
	o.Migrate = true
	st, err := store.OpenWith(ctx, o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "migration :", err)
		return 1
	}
	defer st.Close()
	if len(st.Applied()) == 0 {
		fmt.Printf("✔ %s : schéma à jour\n", target)
		return 0
	}
	fmt.Printf("✔ %s : %d migration(s) appliquée(s) : %s\n", target, len(st.Applied()), strings.Join(st.Applied(), ", "))
	return 0
}

func runCheck(ctx context.Context, args []string) int {
	f := newFlags("check")
	all := f.fs.Bool("all", false, "vérifier toutes les APIs")
	dry := f.fs.Bool("dry-run", false, "ne rien enregistrer (pour tester une nouvelle source avant de merger)")
	f.fs.Parse(args)

	cfg, ok := loadConfig(f.config)
	if !ok {
		return 1
	}
	ids := f.fs.Args()
	if *all {
		ids = ids[:0]
		for _, a := range cfg.APIs {
			ids = append(ids, a.ID)
		}
	}
	if len(ids) == 0 {
		fmt.Fprintln(os.Stderr, "préciser au moins un id d'API, ou --all")
		return 2
	}

	var st *store.Store
	o, _ := dbOptions(f.db, cfg)
	if _, err := os.Stat(o.DSN); !*dry || err == nil || store.DialectOf(o.DSN) == store.Postgres {
		var err error
		if st, err = openStore(ctx, o, cfg); err != nil {
			fmt.Fprintln(os.Stderr, "base de données :", err)
			return 1
		}
		defer st.Close()
	}

	m := monitor.New(cfg, st)
	failed := 0
	for _, id := range ids {
		api, ok := cfg.API(id)
		if !ok {
			fmt.Fprintf(os.Stderr, "API %q absente de la configuration\n", id)
			failed++
			continue
		}
		o, err := m.Check(ctx, id, "manual", *dry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s : erreur interne : %v\n", id, err)
			failed++
			continue
		}
		printOutcome(os.Stdout, api, o, *dry)
		if o.Status == monitor.StatusError {
			failed++
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func printOutcome(w io.Writer, api *config.API, o *monitor.Outcome, dry bool) {
	fmt.Fprintf(w, "\n▶ %s — %s\n", api.ID, api.Name)
	fmt.Fprintf(w, "  source     %s\n", api.Source.URL)
	if o.FetchedURL != "" && o.FetchedURL != api.Source.URL {
		fmt.Fprintf(w, "  redirigé   %s\n", o.FetchedURL)
	}
	if o.Spec != nil {
		fmt.Fprintf(w, "  contrat    %s · « %s » version %s · %d opérations\n", o.Spec.Format, o.Spec.Title, o.Spec.Version, o.Spec.OperationCount)
		fmt.Fprintf(w, "  empreinte  sha256:%s\n", o.Spec.SHA256[:16])
	}
	suffix := ""
	if dry {
		suffix = " (dry-run : rien n'a été enregistré)"
	}
	switch o.Status {
	case monitor.StatusError:
		fmt.Fprintf(w, "  ✘ ERREUR [%s] %v%s\n", o.ErrorKind, o.Err, suffix)
		if o.ConsecutiveErrors > 0 {
			fmt.Fprintf(w, "    %d échec(s) consécutif(s)\n", o.ConsecutiveErrors)
		}
	case monitor.StatusUnchanged:
		how := "contrat identique"
		if o.NotModified {
			how = "304 Not Modified"
		}
		fmt.Fprintf(w, "  ✔ inchangé (%s, %s)%s\n", how, o.Duration.Round(1e6), suffix)
	case monitor.StatusChanged:
		s := o.Summary
		fmt.Fprintf(w, "  ● CHANGEMENT : %d cassant(s), %d à surveiller, %d info — %d dans le périmètre (%s)%s\n",
			s.Errors, s.Warns, s.Infos, s.InScope, o.Duration.Round(1e6), suffix)
		const max = 25
		for i, it := range s.Items {
			if i == max {
				fmt.Fprintf(w, "    … et %d autre(s)\n", len(s.Items)-max)
				break
			}
			fmt.Fprintf(w, "    %s\n", formatItem(it))
		}
	}
}

func formatItem(it diff.Item) string {
	label := map[string]string{diff.LevelError: "CASSANT ", diff.LevelWarn: "ATTENTION", diff.LevelInfo: "info    "}[it.Level]
	op := strings.TrimSpace(it.Method + " " + it.Path)
	if op != "" {
		op += " — "
	}
	scope := ""
	if !it.InScope {
		scope = " (hors périmètre)"
	}
	return fmt.Sprintf("[%s] %s%s%s", label, op, it.Text, scope)
}

func runServe(ctx context.Context, args []string) int {
	f := newFlags("serve")
	listen := f.fs.String("listen", os.Getenv("KANSABOOJ_LISTEN"), "adresse d'écoute (défaut : server.listen, :8080 ; env KANSABOOJ_LISTEN)")
	watch := f.fs.Duration("watch-config", 30*time.Second, "intervalle de détection des modifications de configuration (0 = désactivé)")
	debug := f.fs.Bool("debug", os.Getenv("KANSABOOJ_DEBUG") != "", "logs détaillés (env KANSABOOJ_DEBUG)")
	f.fs.Parse(args)

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	cfg, ok := loadConfig(f.config)
	if !ok {
		return 1
	}
	o, target := dbOptions(f.db, cfg)
	st, err := openStore(ctx, o, cfg)
	if err != nil {
		log.Error("base de données", "target", target, "err", err)
		return 1
	}
	defer st.Close()
	sch, err := scheduler.New(f.config, cfg, st, log)
	if err != nil {
		log.Error("initialisation", "err", err)
		return 1
	}

	addr := cfg.Server.Listen
	if *listen != "" {
		addr = *listen
	}
	srv := &http.Server{Addr: addr, Handler: httpapi.New(sch, st, log).Handler(), ReadHeaderTimeout: 10 * time.Second}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		hup := make(chan os.Signal, 1)
		signal.Notify(hup, syscall.SIGHUP)
		for {
			select {
			case <-ctx.Done():
				return
			case <-hup:
				if err := sch.Reload(ctx); err != nil {
					log.Error("rechargement refusé, configuration précédente conservée", "err", err)
				}
			}
		}
	}()
	if *watch > 0 {
		go sch.WatchConfig(ctx, *watch)
	}
	schedDone := make(chan error, 1)
	go func() { schedDone <- sch.Run(ctx) }()
	go func() {
		<-ctx.Done()
		shut, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		srv.Shutdown(shut)
	}()

	log.Info("kansabooj démarré", "listen", addr, "apis", len(cfg.APIs), "version", httpapi.Version, "database", target)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("serveur HTTP", "err", err)
		cancel()
		<-schedDone
		return 1
	}
	cancel()
	if err := <-schedDone; err != nil {
		log.Error("scheduler", "err", err)
		return 1
	}
	log.Info("arrêt propre")
	return 0
}

func runHistory(ctx context.Context, args []string) int {
	f := newFlags("history")
	limit := f.fs.Int("n", 20, "nombre d'évolutions affichées")
	f.fs.Parse(args)
	if f.fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage : kansabooj history <api-id>")
		return 2
	}
	cfg, ok := loadConfig(f.config)
	if !ok {
		return 1
	}
	o, _ := dbOptions(f.db, cfg)
	st, err := openStore(ctx, o, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "base de données :", err)
		return 1
	}
	defer st.Close()
	tl, err := st.Timeline(ctx, f.fs.Arg(0), *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(tl) == 0 {
		fmt.Println("aucune évolution enregistrée")
		return 0
	}
	for _, c := range tl {
		ver := c.VersionTo
		if c.VersionFrom != "" && c.VersionFrom != c.VersionTo {
			ver = c.VersionFrom + " → " + c.VersionTo
		}
		fmt.Printf("#%-5d %s  %-22s cassant:%-3d attention:%-3d info:%-3d périmètre:%d\n",
			c.ID, c.DetectedAt.Local().Format("2006-01-02 15:04"), ver, c.Errors, c.Warns, c.Infos, c.InScope)
	}
	return 0
}
