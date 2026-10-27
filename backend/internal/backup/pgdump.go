package backup

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// ExecFunc runs a program and returns its combined output. env is added to
// the process environment.
type ExecFunc func(ctx context.Context, env []string, name string, args ...string) ([]byte, error)

// OSExec is the production ExecFunc.
func OSExec(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.WaitDelay = 5 * time.Second
	return cmd.CombinedOutput()
}

var pgDumpVersion = regexp.MustCompile(`(\d+)(?:\.\d+)*`)

// excludedAdminSchema is the privileged schema of the role-admin bootstrap. It
// holds instance specific owners, definer functions and literals that a
// non-superuser restore cannot recreate, and the app role cannot read it; the
// bootstrap script recreates it.
const excludedAdminSchema = "hatamoto_admin"

// checkTool refuses a pg_dump that is not usable or whose major version is not
// the server's (the dump would not restore with a matching pg_restore).
func (e *Engine) checkTool(ctx context.Context) (string, error) {
	out, err := e.run(ctx, nil, "pg_dump", "--version")
	if err != nil {
		return "", fmt.Errorf("pg_dump is not usable: %w", err)
	}
	version := strings.TrimSpace(string(out))
	m := pgDumpVersion.FindStringSubmatch(version)
	if m == nil {
		return "", fmt.Errorf("cannot read the pg_dump version from %q", version)
	}
	tool, _ := strconv.Atoi(m[1])
	server, err := e.serverMajor(ctx)
	if err != nil {
		return "", fmt.Errorf("cannot read the server version: %w", err)
	}
	if tool < 10 || tool != server {
		return "", fmt.Errorf("pg_dump %d does not match server %d", tool, server)
	}
	return version, nil
}

// dumpCore dumps everything but the admin schema and the plugin schemas.
func (e *Engine) dumpCore(ctx context.Context, out string, plugins []Plugin) error {
	args := []string{"--format=custom", "--file=" + out, "--exclude-schema=" + excludedAdminSchema}
	for _, p := range plugins {
		args = append(args, "--exclude-schema="+p.Schema)
	}
	return e.pgDump(ctx, "core", args)
}

// dumpPlugin dumps one plugin schema as its owner role: the app role holds the
// plugin role without inheriting it, so only SET ROLE can read the schema.
func (e *Engine) dumpPlugin(ctx context.Context, out string, p Plugin) error {
	args := []string{"--format=custom", "--file=" + out, "--role=" + p.Role, "--schema=" + p.Schema}
	return e.pgDump(ctx, p.Schema, args)
}

func (e *Engine) pgDump(ctx context.Context, part string, args []string) error {
	env, err := pgEnv("DATABASE_URL", e.databaseURL, false)
	if err != nil {
		return err
	}
	if out, err := e.run(ctx, env, "pg_dump", args...); err != nil {
		return fmt.Errorf("%s: %s", part, errorText(out, err))
	}
	return nil
}

func errorText(out []byte, err error) string {
	if s := ErrorLine(out); s != "" {
		return s
	}
	return err.Error()
}

const maxErrorLine = 300

// ErrorLine is what to show the owner of a failed pg_dump: the first
// "pg_dump: error:" line (plus a directly following detail line), because the
// last lines are often the LOCK TABLE query that follows it. Without one, the
// last non-empty line.
func ErrorLine(out []byte) string {
	lines := strings.Split(string(out), "\n")
	for i, l := range lines {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "pg_dump: error:") {
			continue
		}
		if i+1 < len(lines) {
			if next := strings.TrimSpace(lines[i+1]); strings.HasPrefix(next, "pg_dump: detail:") {
				l += "; " + next
			}
		}
		return capLine(l)
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return capLine(l)
		}
	}
	return ""
}

func capLine(s string) string {
	if len(s) > maxErrorLine {
		return s[:maxErrorLine]
	}
	return s
}

// pgURLKeys maps the URL query keys that have a libpq environment variable.
var pgURLKeys = map[string]string{
	"sslmode": "PGSSLMODE", "sslrootcert": "PGSSLROOTCERT", "sslcert": "PGSSLCERT",
	"sslkey": "PGSSLKEY", "sslcrl": "PGSSLCRL", "sslcertmode": "PGSSLCERTMODE",
	"sslsni": "PGSSLSNI", "ssl_min_protocol_version": "PGSSLMINPROTOCOLVERSION",
	"ssl_max_protocol_version": "PGSSLMAXPROTOCOLVERSION",
	"gssencmode":               "PGGSSENCMODE", "gsslib": "PGGSSLIB", "krbsrvname": "PGKRBSRVNAME",
	"channel_binding": "PGCHANNELBINDING", "connect_timeout": "PGCONNECT_TIMEOUT",
	"application_name": "PGAPPNAME", "options": "PGOPTIONS",
	"target_session_attrs": "PGTARGETSESSIONATTRS", "passfile": "PGPASSFILE",
	"requirepeer": "PGREQUIREPEER", "client_encoding": "PGCLIENTENCODING",
	"load_balance_hosts": "PGLOADBALANCEHOSTS", "require_auth": "PGREQUIREAUTH",
	"sslnegotiation": "PGSSLNEGOTIATION", "min_protocol_version": "PGMINPROTOCOLVERSION",
	"max_protocol_version": "PGMAXPROTOCOLVERSION",
}

// pgConnKeys are read through pgconn.ParseConfig, not mapped one by one.
var pgConnKeys = map[string]bool{"host": true, "port": true, "user": true, "password": true, "dbname": true}

// pgxOnlyKey reports the keys only pgx and pgxpool read. They tune the client
// and do not change which server is reached, so the tools do not need them.
func pgxOnlyKey(k string) bool {
	switch k {
	case "default_query_exec_mode", "statement_cache_capacity", "description_cache_capacity", "min_read_buffer_size":
		return true
	}
	return strings.HasPrefix(k, "pool_")
}

// pgEnv turns a postgres URL into the libpq environment variables, so the
// password never shows in a process listing. It reads the URL the way pgx
// does, so the checks (pgx) and the tools (psql, pg_dump, pg_restore) reach
// the same server. Keys with a libpq variable are mapped and pgx-only keys are
// dropped. Any other key (a server setting such as search_path, or
// sslpassword) the tools cannot be given. Restore (strict) writes to exactly
// one server it has checked, so it refuses those, a list of hosts, and
// service/servicefile. Backup create (lenient) accepts all of them as it
// always has, so a URL that serves keeps backing up: it passes a host list on
// as PGHOST/PGPORT lists, which libpq reads like pgx, and ignores service
// (pgx has already folded the service's destination into the values exported).
// name is the setting the URL came from, used in errors.
func pgEnv(name, raw string, strict bool) ([]string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, fmt.Errorf("%s is not a postgres URL", name)
	}
	q := u.Query()
	for k := range q {
		switch {
		case (k == "service" || k == "servicefile") && !strict:
		case k == "service" || k == "servicefile":
			return nil, fmt.Errorf("%s has the query parameter %q, which the postgres tools cannot be given; remove it", name, k)
		case pgConnKeys[k], pgxOnlyKey(k):
		default:
			if _, ok := pgURLKeys[k]; !ok && strict {
				return nil, fmt.Errorf("%s has the query parameter %q, which the postgres tools cannot be given; remove it", name, k)
			}
		}
	}
	cfg, err := pgconn.ParseConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("%s is not usable: %s", name, errorLine(err))
	}
	hosts, ports := hostLists(cfg)
	if strict && len(hosts) > 1 {
		return nil, fmt.Errorf("%s lists several hosts, which restore cannot be given: it must write to the one server it checked", name)
	}
	var env []string
	add := func(k, v string) {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}
	add("PGHOST", strings.Join(hosts, ","))
	add("PGPORT", strings.Join(ports, ","))
	add("PGUSER", cfg.User)
	add("PGDATABASE", cfg.Database)
	// Only a password written in the URL is passed on. pgx also reads
	// PGPASSWORD and .pgpass, which the tools read themselves.
	if _, ok := u.User.Password(); ok || q.Has("password") {
		add("PGPASSWORD", cfg.Password)
	}
	for k, v := range pgURLKeys {
		add(v, q.Get(k))
	}
	sort.Strings(env)
	return env, nil
}

// hostLists returns the hosts and ports pgx will try, in order, as the comma
// lists libpq reads from PGHOST and PGPORT. pgx adds a fallback entry per host
// for TLS (sslmode=prefer), so a repeated host and port pair is dropped. The
// ports list is exported whole, so a list with different ports keeps each one.
func hostLists(cfg *pgconn.Config) (hosts, ports []string) {
	seen := map[string]bool{}
	add := func(h string, p uint16) {
		key := h + "\x00" + strconv.Itoa(int(p))
		if seen[key] {
			return
		}
		seen[key] = true
		hosts = append(hosts, h)
		if p != 0 {
			ports = append(ports, strconv.Itoa(int(p)))
		} else {
			ports = append(ports, "")
		}
	}
	add(cfg.Host, cfg.Port)
	for _, f := range cfg.Fallbacks {
		add(f.Host, f.Port)
	}
	if len(hosts) == 1 && ports[0] == "" {
		ports = nil
	}
	return hosts, ports
}
