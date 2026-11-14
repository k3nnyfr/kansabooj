package config

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"time"
)

// Database describes the PostgreSQL server, typically a third-party / shared server
// operated by a DBA team. It is ignored when KANSABOOJ_DB (or --db) is set, except
// for the schema and pool settings, which still apply.
type Database struct {
	URL             string   `yaml:"url"` // full postgres:// URL, alternative to host/port/name/user/password
	Host            string   `yaml:"host"`
	Port            int      `yaml:"port"`
	Name            string   `yaml:"name"`
	User            string   `yaml:"user"`
	Password        string   `yaml:"password"`    // ${ENV} or ${file:...}, never in clear
	SSLMode         string   `yaml:"sslmode"`     // disable | require | verify-ca | verify-full
	SSLRootCert     string   `yaml:"sslrootcert"` // CA of the server certificate (verify-ca / verify-full)
	SSLCert         string   `yaml:"sslcert"`     // client certificate (mutual TLS)
	SSLKey          string   `yaml:"sslkey"`
	Schema          string   `yaml:"schema"` // dedicated schema on a shared database (search_path)
	ApplicationName string   `yaml:"application_name"`
	ConnectTimeout  Duration `yaml:"connect_timeout"`
	MaxOpenConns    int      `yaml:"max_open_conns"`
	MaxIdleConns    int      `yaml:"max_idle_conns"`
	ConnMaxLifetime Duration `yaml:"conn_max_lifetime"` // recycle connections (PgBouncer, load balancers, failovers)
	Migrate         *bool    `yaml:"migrate"`           // false: the schema is migrated separately (kansabooj migrate)
}

var (
	sslModes      = []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}
	schemaPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
)

// Configured reports whether the section designates a server.
func (d *Database) Configured() bool { return d.URL != "" || d.Host != "" }

// AutoMigrate reports whether migrations run at startup.
func (d *Database) AutoMigrate() bool { return d.Migrate == nil || *d.Migrate }

func (d *Database) applyDefaults() {
	if d.Port == 0 {
		d.Port = 5432
	}
	if d.SSLMode == "" && d.Host != "" {
		d.SSLMode = "require"
		if d.SSLRootCert != "" {
			d.SSLMode = "verify-full"
		}
	}
	if d.ApplicationName == "" {
		d.ApplicationName = "kansabooj"
	}
	if d.ConnectTimeout == 0 {
		d.ConnectTimeout = Duration(10 * time.Second)
	}
	if d.MaxOpenConns == 0 {
		d.MaxOpenConns = 10
	}
	if d.MaxIdleConns == 0 {
		d.MaxIdleConns = 5
	}
	if d.ConnMaxLifetime == 0 {
		d.ConnMaxLifetime = Duration(30 * time.Minute)
	}
}

func (d *Database) validate() []string {
	var p []string
	add := func(f string, a ...any) { p = append(p, "database : "+fmt.Sprintf(f, a...)) }
	if d.URL != "" && d.Host != "" {
		add("url et host sont exclusifs")
	}
	if d.URL != "" {
		if u, err := url.Parse(d.URL); err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
			add("url : postgres://… attendu")
		}
	}
	if d.Host != "" {
		if d.Name == "" {
			add("name obligatoire avec host")
		}
		if d.User == "" {
			add("user obligatoire avec host")
		}
	}
	if d.Port < 0 || d.Port > 65535 {
		add("port invalide %d", d.Port)
	}
	if d.SSLMode != "" && !slices.Contains(sslModes, d.SSLMode) {
		add("sslmode %q inconnu (disable, require, verify-ca, verify-full)", d.SSLMode)
	}
	if (d.SSLCert == "") != (d.SSLKey == "") {
		add("sslcert et sslkey vont ensemble")
	}
	if d.Schema != "" && !schemaPattern.MatchString(d.Schema) {
		add("schema %q invalide (minuscules, chiffres, _)", d.Schema)
	}
	if d.MaxOpenConns < 4 {
		add("max_open_conns doit être ≥ 4 (verrou leader, verrous de check et requêtes partagent le pool)")
	}
	if d.MaxIdleConns > d.MaxOpenConns {
		add("max_idle_conns (%d) > max_open_conns (%d)", d.MaxIdleConns, d.MaxOpenConns)
	}
	return p
}

// DSN builds the connection URL from the discrete fields (or returns URL).
func (d *Database) DSN() string {
	if d.URL != "" {
		return d.URL
	}
	u := &url.URL{Scheme: "postgres", Host: d.Host + ":" + strconv.Itoa(d.Port), Path: "/" + d.Name}
	if d.Password != "" {
		u.User = url.UserPassword(d.User, d.Password)
	} else {
		u.User = url.User(d.User)
	}
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("sslmode", d.SSLMode)
	set("sslrootcert", d.SSLRootCert)
	set("sslcert", d.SSLCert)
	set("sslkey", d.SSLKey)
	set("connect_timeout", strconv.Itoa(int(d.ConnectTimeout.Std().Seconds())))
	u.RawQuery = q.Encode()
	return u.String()
}

// Redacted describes the target without secrets, for logs.
func (d *Database) Redacted() string {
	if d.URL != "" {
		if u, err := url.Parse(d.URL); err == nil {
			return u.Redacted()
		}
		return "postgres://(url invalide)"
	}
	return fmt.Sprintf("postgres://%s@%s:%d/%s (sslmode=%s)", d.User, d.Host, d.Port, d.Name, d.SSLMode)
}
