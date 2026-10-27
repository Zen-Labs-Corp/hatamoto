package backup

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakePgDump answers --version and writes "dump" to the --file argument. With
// failOut set it fails with that output instead.
type fakePgDump struct {
	version string
	failOut string
	calls   [][]string
	env     [][]string
}

func (f *fakePgDump) run(_ context.Context, env []string, _ string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	f.env = append(f.env, env)
	if len(args) == 1 && args[0] == "--version" {
		return []byte("pg_dump (PostgreSQL) " + f.version + "\n"), nil
	}
	if f.failOut != "" {
		return []byte(f.failOut), errors.New("exit status 1")
	}
	for _, a := range args {
		if p, ok := strings.CutPrefix(a, "--file="); ok {
			return nil, os.WriteFile(p, []byte("dump"), 0o600)
		}
	}
	return nil, errors.New("no --file")
}

const testURL = "postgres://u:secret@db.example:5433/site?sslmode=require"

func newEngine(f *fakePgDump, server int) *Engine {
	return &Engine{run: f.run, databaseURL: testURL,
		serverMajor: func(context.Context) (int, error) { return server, nil }}
}

func TestErrorLine(t *testing.T) {
	long := strings.Repeat("x", 500)
	cases := map[string]struct{ in, want string }{
		"error line first, lock last": {
			"pg_dump: error: query failed: ERROR:  permission denied for table t\npg_dump: detail: Query was: LOCK TABLE public.t IN ACCESS SHARE MODE\nLOCK TABLE public.t IN ACCESS SHARE MODE\n",
			"pg_dump: error: query failed: ERROR:  permission denied for table t; pg_dump: detail: Query was: LOCK TABLE public.t IN ACCESS SHARE MODE",
		},
		"error line alone": {"warn\npg_dump: error: boom\nlast\n", "pg_dump: error: boom"},
		"no error line":    {"first\n\nconnection refused\n\n", "connection refused"},
		"long line capped": {"pg_dump: error: " + long, ("pg_dump: error: " + long)[:300]},
		"empty":            {"", ""},
	}
	for name, c := range cases {
		if got := ErrorLine([]byte(c.in)); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
}

func TestDumpCoreExcludesAdminAndEveryPlugin(t *testing.T) {
	f := &fakePgDump{version: "16.2"}
	e := newEngine(f, 16)
	out := t.TempDir() + "/core.dump"
	plugins := []Plugin{{Schema: "plugin_a", Role: "hm_p_1_a"}, {Schema: "plugin_b_c", Role: "hm_p_1_b_c"}}
	if err := e.dumpCore(t.Context(), out, plugins); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(f.calls[0], " ")
	for _, want := range []string{"--format=custom", "--file=" + out, "--exclude-schema=hatamoto_admin", "--exclude-schema=plugin_a", "--exclude-schema=plugin_b_c"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	if strings.Contains(args, "--role") {
		t.Errorf("core dump must not switch role: %s", args)
	}
}

func TestDumpPluginUsesRoleAndKeepsPasswordOutOfArgs(t *testing.T) {
	f := &fakePgDump{version: "16.2"}
	e := newEngine(f, 16)
	out := t.TempDir() + "/plugin_a.dump"
	if err := e.dumpPlugin(t.Context(), out, Plugin{Name: "a", Schema: "plugin_a", Role: "hm_p_1_a"}); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(f.calls[0], " ")
	for _, want := range []string{"--role=hm_p_1_a", "--schema=plugin_a"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	for _, call := range f.calls {
		if strings.Contains(strings.Join(call, " "), "secret") {
			t.Fatalf("password in args: %v", call)
		}
	}
	env := strings.Join(f.env[0], " ")
	for _, want := range []string{"PGHOST=db.example", "PGPORT=5433", "PGUSER=u", "PGPASSWORD=secret", "PGDATABASE=site", "PGSSLMODE=require"} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %s in %s", want, env)
		}
	}
}

func TestDumpFailureShowsErrorLineNotTail(t *testing.T) {
	f := &fakePgDump{version: "16.2", failOut: "pg_dump: error: query failed: permission denied\npg_dump: detail: Query was: LOCK TABLE x\nLOCK TABLE x IN ACCESS SHARE MODE\n"}
	e := newEngine(f, 16)
	err := e.dumpCore(t.Context(), t.TempDir()+"/core.dump", nil)
	if err == nil || !strings.HasPrefix(err.Error(), "core: pg_dump: error: query failed: permission denied") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckTool(t *testing.T) {
	cases := map[string]struct {
		version string
		server  int
		wantErr string
	}{
		"match":    {"16.2", 16, ""},
		"mismatch": {"15.4", 16, "does not match"},
		"too old":  {"9.6.1", 9, "does not match"},
	}
	for name, c := range cases {
		_, err := newEngine(&fakePgDump{version: c.version}, c.server).checkTool(t.Context())
		if c.wantErr == "" && err != nil || c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%s: err = %v, want %q", name, err, c.wantErr)
		}
	}
}

func TestPgEnvRejectsNonPostgres(t *testing.T) {
	if _, err := pgEnv("DATABASE_URL", "mysql://x", true); err == nil {
		t.Fatal("want error")
	}
}

// A URL without a password must leave the inherited PGPASSWORD (or .pgpass)
// alone: docs tell operators to pass the superuser password that way.
func TestPgEnvWithoutPasswordKeepsInheritedPGPASSWORD(t *testing.T) {
	env, err := pgEnv("DATABASE_URL", "postgres://su@db.example/postgres", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range env {
		if strings.HasPrefix(e, "PGPASSWORD") {
			t.Fatalf("pgEnv sets %q", e)
		}
	}
	t.Setenv("PGPASSWORD", "from-the-shell")
	out, err := OSExec(t.Context(), env, "sh", "-c", "printf %s \"$PGPASSWORD\"")
	if err != nil || string(out) != "from-the-shell" {
		t.Fatalf("child saw PGPASSWORD %q, %v", out, err)
	}
}

// pgx reads host and port from the query; the tools must get them too, or the
// checks and the tools reach different servers.
func TestPgEnvMapsQueryHostAndPort(t *testing.T) {
	env, err := pgEnv("DATABASE_URL", "postgres://hm@/site?host=/tmp&port=5433&sslmode=require&sslrootcert=/etc/ca.pem&sslcert=/c.pem&sslkey=/k.pem", true)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(env, " ")
	for _, want := range []string{"PGHOST=/tmp", "PGPORT=5433", "PGUSER=hm", "PGDATABASE=site", "PGSSLMODE=require", "PGSSLROOTCERT=/etc/ca.pem", "PGSSLCERT=/c.pem", "PGSSLKEY=/k.pem"} {
		if !strings.Contains(got, want) {
			t.Errorf("env %q lacks %s", got, want)
		}
	}
}

func TestPgEnvRestoreRefusesWhatItCannotMap(t *testing.T) {
	for _, raw := range []string{
		"postgres://u@h/db?search_path=x",
		"postgres://u@h/db?sslpassword=x",
		"postgres://u@h/db?service=s",
		"postgres://u@h/db?servicefile=/f",
		"postgres://u@h1,h2/db",
	} {
		_, err := pgEnv("--admin-url", raw, true)
		if err == nil || !strings.HasPrefix(err.Error(), "--admin-url ") {
			t.Errorf("%s: err = %v, want a refusal naming --admin-url", raw, err)
		}
	}
}

// service and servicefile are ignored on create as on main, and the values pgx
// resolved from the URL reach pg_dump.
func TestPgDumpIgnoresServiceKeys(t *testing.T) {
	svc := filepath.Join(t.TempDir(), "svc.conf")
	if err := os.WriteFile(svc, []byte("[x]\nsslmode=require\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, err := pgEnv("DATABASE_URL", "postgres://u@db.example:5433/site?service=x&servicefile="+svc, false)
	if err != nil {
		t.Fatal(err)
	}
	got := "\n" + strings.Join(env, "\n") + "\n"
	for _, w := range []string{"PGHOST=db.example", "PGPORT=5433", "PGUSER=u", "PGDATABASE=site"} {
		if !strings.Contains(got, "\n"+w+"\n") {
			t.Errorf("env %q lacks %s", env, w)
		}
	}
	if _, err := pgEnv("DATABASE_URL", "postgres://u@db.example/site?service=x&servicefile="+svc, true); err == nil {
		t.Error("restore must refuse service")
	}
}

// mainAccepts is origin/main's acceptance rule for pgEnv, the oracle for the
// differential test: any postgres URL that parses.
func mainAccepts(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql")
}

// The create path accepts what main accepted and serve (pgx) can use, and a
// host list reaches libpq as PGHOST/PGPORT lists.
func TestPgEnvCreateAcceptsWhatMainAccepted(t *testing.T) {
	svc := filepath.Join(t.TempDir(), "svc.conf")
	if err := os.WriteFile(svc, []byte("[x]\nsslmode=require\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		raw  string
		want []string
	}{
		{"postgres://u:pw@h1,h2/db", []string{"PGHOST=h1,h2"}},
		{"postgres://u:pw@h1,h2:5433/db", []string{"PGHOST=h1,h2", "PGPORT=5432,5433"}},
		{"postgres://u:pw@h1:5432,h2:5433/db?target_session_attrs=read-write", []string{"PGHOST=h1,h2", "PGPORT=5432,5433", "PGTARGETSESSIONATTRS=read-write"}},
		{"postgres://u:pw@h1,h2/db?sslmode=disable", []string{"PGHOST=h1,h2", "PGSSLMODE=disable"}},
		{"postgres://u:pw@h1,h2/db?sslmode=require", []string{"PGHOST=h1,h2"}},
		{"postgres://u:pw@/db?host=a,b&port=1,2", []string{"PGHOST=a,b", "PGPORT=1,2"}},
		{"postgres://u@h/db?service=x&servicefile=" + svc, []string{"PGHOST=h", "PGUSER=u", "PGDATABASE=db"}},
		{"postgres://u@h/db?require_auth=scram-sha-256&sslnegotiation=direct&min_protocol_version=3.0&max_protocol_version=latest",
			[]string{"PGREQUIREAUTH=scram-sha-256", "PGSSLNEGOTIATION=direct", "PGMINPROTOCOLVERSION=3.0", "PGMAXPROTOCOLVERSION=latest"}},
		{"postgres://hatamoto:s3cret@db:5432/hatamoto?sslmode=disable", []string{"PGHOST=db"}},
		{"postgres://u@[::1]:5433/db", []string{"PGHOST=::1", "PGPORT=5433"}},
		{"postgres://u@h/db?search_path=x&sslpassword=y&pool_max_conns=3", []string{"PGHOST=h"}},
	}
	for _, c := range cases {
		if !mainAccepts(c.raw) {
			t.Fatalf("%s: the oracle rejects it", c.raw)
		}
		env, err := pgEnv("DATABASE_URL", c.raw, false)
		if err != nil {
			t.Errorf("%s: main accepted it, create refuses: %v", c.raw, err)
			continue
		}
		got := "\n" + strings.Join(env, "\n") + "\n"
		for _, w := range c.want {
			if !strings.Contains(got, "\n"+w+"\n") {
				t.Errorf("%s: env %q lacks %s", c.raw, env, w)
			}
		}
	}
}

// Restore maps the keys that have a libpq variable, including the newer ones.
func TestPgEnvStrictMapsNewerLibpqKeys(t *testing.T) {
	env, err := pgEnv("--admin-url", "postgres://u@h/db?require_auth=scram-sha-256&sslnegotiation=direct&min_protocol_version=3.0&max_protocol_version=latest", true)
	if err != nil {
		t.Fatal(err)
	}
	got := "\n" + strings.Join(env, "\n") + "\n"
	for _, w := range []string{"PGREQUIREAUTH=scram-sha-256", "PGSSLNEGOTIATION=direct", "PGMINPROTOCOLVERSION=3.0", "PGMAXPROTOCOLVERSION=latest"} {
		if !strings.Contains(got, "\n"+w+"\n") {
			t.Errorf("env %q lacks %s", env, w)
		}
	}
}

// Every URL that serve accepts must keep backing up (the pre-update backup
// blocks the update when it fails), and the libpq settings must reach the tools.
func TestPgEnvAcceptsEveryServeURL(t *testing.T) {
	for _, k := range []string{"PGHOST", "PGPORT", "PGUSER", "PGDATABASE", "PGSSLMODE"} {
		t.Setenv(k, "")
	}
	cases := []struct {
		raw     string
		want    []string
		dropped []string // env names that must not be set
		strict  bool     // restore also accepts it
	}{
		{"postgres://hatamoto:s3cret@db:5432/hatamoto?sslmode=disable", []string{"PGHOST=db", "PGPORT=5432", "PGSSLMODE=disable"}, nil, true},
		{"postgres://hatamoto:CHANGE-ME@127.0.0.1:5432/hatamoto?sslmode=disable", []string{"PGHOST=127.0.0.1", "PGPASSWORD=CHANGE-ME", "PGUSER=hatamoto"}, nil, true},
		{"postgres://hatamoto:hatamoto@localhost:5432/hatamoto", []string{"PGDATABASE=hatamoto"}, []string{"PGSSLMODE"}, true},
		{"postgresql://hatamoto_demo:CHANGE_ME@127.0.0.1:5442/hatamoto_demo?sslmode=disable", []string{"PGPORT=5442"}, nil, true},
		{"postgres://postgres@host/postgres", []string{"PGHOST=host", "PGUSER=postgres"}, []string{"PGPASSWORD"}, true},
		{"postgres://u:pw@h/db?sslmode=require&connect_timeout=10", []string{"PGSSLMODE=require", "PGCONNECT_TIMEOUT=10"}, nil, true},
		{"postgres://u@h/db?application_name=hatamoto", []string{"PGAPPNAME=hatamoto"}, nil, true},
		{"postgres://u@h/db?options=-c%20search_path%3Dx", []string{"PGOPTIONS=-c search_path=x"}, nil, true},
		{"postgres://u@h/db?target_session_attrs=read-write&passfile=/p&gssencmode=disable&channel_binding=prefer&krbsrvname=k&sslcrl=/crl",
			[]string{"PGTARGETSESSIONATTRS=read-write", "PGPASSFILE=/p", "PGGSSENCMODE=disable", "PGCHANNELBINDING=prefer", "PGKRBSRVNAME=k", "PGSSLCRL=/crl"}, nil, true},
		{"postgres://u@h/db?pool_max_conns=5&pool_min_conns=1&pool_max_conn_lifetime=1h&pool_max_conn_idle_time=1m&pool_health_check_period=1m&pool_max_conn_lifetime_jitter=1s&default_query_exec_mode=simple_protocol&statement_cache_capacity=0&description_cache_capacity=0&min_read_buffer_size=1024",
			[]string{"PGHOST=h"}, nil, true},
		{"postgres://u@[::1]:5433/db", []string{"PGHOST=::1", "PGPORT=5433"}, nil, true},
		{"postgres://u@[::1]/db", []string{"PGHOST=::1", "PGPORT=5432"}, nil, true},
		{"postgres://u@/db?host=/var/run/postgresql", []string{"PGHOST=/var/run/postgresql"}, nil, true},
		{"postgres://u:p%40%3A%2F%23%3Fx@h/db", []string{"PGPASSWORD=p@:/#?x"}, nil, true},
		// Server settings and sslpassword cannot be given to the tools: restore
		// refuses them, backup create ignores them.
		{"postgres://u@h/db?search_path=x", []string{"PGHOST=h"}, nil, false},
		{"postgres://u@h/db?sslpassword=x&pool_max_conns=5", []string{"PGHOST=h"}, nil, false},
	}
	for _, c := range cases {
		env, err := pgEnv("DATABASE_URL", c.raw, false)
		if err != nil {
			t.Errorf("%s: create path refused: %v", c.raw, err)
			continue
		}
		got := "\n" + strings.Join(env, "\n") + "\n"
		for _, w := range c.want {
			if !strings.Contains(got, "\n"+w+"\n") {
				t.Errorf("%s: env %q lacks %s", c.raw, env, w)
			}
		}
		for _, d := range c.dropped {
			if strings.Contains(got, "\n"+d) {
				t.Errorf("%s: env %q sets %s", c.raw, env, d)
			}
		}
		if _, err := pgEnv("DATABASE_URL", c.raw, true); (err == nil) != c.strict {
			t.Errorf("%s: restore err = %v, want accepted=%v", c.raw, err, c.strict)
		}
	}
}

// The pgx-only keys never reach the environment.
func TestPgEnvDropsPgxOnlyKeys(t *testing.T) {
	env, err := pgEnv("DATABASE_URL", "postgres://u@h/db?pool_max_conns=5&default_query_exec_mode=simple_protocol", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 4 {
		t.Fatalf("env = %q", env)
	}
}
