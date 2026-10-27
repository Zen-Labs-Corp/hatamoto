package backup

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	stepsAppURL   = "postgres://hm_app:apppw@db.example:5433/site?sslmode=disable"
	stepsAdminURL = "postgres://su:adminpw@db.example:5433/postgres?sslmode=disable"
)

const pluginListing = `;
; Archive created at 2026-10-25
2615; 1259 1 SCHEMA - plugin_forms hm_p_1a2b3c4d_forms
1259; 1259 2 TABLE plugin_forms deliveries hm_p_1a2b3c4d_forms
`

// stepsRig records every exec, provision and owner lookup in one call log and
// captures the files the tools would read at call time.
type stepsRig struct {
	d     *dbRestorer
	log   []string
	calls [][]string
	envs  [][]string
	seen  map[string]string // tool and flag, or "dump <name>", to the file content seen at call time
	paths map[string]string // label to file path
	fail  map[string]error  // first arg or tool name to a failure
}

func newStepsRig(t *testing.T, mut func(*archSpec)) *stepsRig {
	t.Helper()
	arch := buildArchive(t, mut)
	sc, err := scanArchive(arch)
	if err != nil {
		t.Fatal(err)
	}
	r := &stepsRig{seen: map[string]string{}, paths: map[string]string{}, fail: map[string]error{}}
	rr := &restorer{
		o:   RestoreOptions{DatabaseURL: stepsAppURL},
		req: RestoreRequest{AdminURL: stepsAdminURL},
		src: arch, scan: sc, mf: sc.mf,
	}
	rr.run = func(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
		r.calls = append(r.calls, append([]string{name}, args...))
		r.envs = append(r.envs, env)
		label := name
		if name == "pg_restore" && len(args) > 0 && args[0] == "-l" {
			label = "pg_restore-l"
		}
		r.log = append(r.log, label)
		// Remember what the files named in the args hold right now.
		for i, a := range args {
			if (a == "-f" || a == "-L") && i+1 < len(args) {
				b, err := os.ReadFile(args[i+1])
				if err != nil {
					t.Errorf("%s reads %s: %v", name, args[i+1], err)
				}
				r.seen[name+" "+a] = string(b)
				r.paths[name+" "+a] = args[i+1]
			}
		}
		if name == "pg_restore" {
			last := args[len(args)-1]
			if b, err := os.ReadFile(last); err == nil {
				r.seen["dump "+strings.TrimSuffix(last[strings.LastIndex(last, "/")+1:], ".dump")] = string(b)
			}
			r.paths["workdir"] = last[:strings.LastIndex(last, "/")]
		}
		if err, ok := r.fail[label]; ok {
			return []byte("pg_restore: error: boom\nCommand was: x\n"), err
		}
		if label == "pg_restore-l" {
			return []byte(pluginListing), nil
		}
		return nil, nil
	}
	r.d = &dbRestorer{restorer: rr,
		provision: func(_ context.Context, name string) error {
			r.log = append(r.log, "provision "+name)
			return r.fail["provision"]
		},
		ownerOf: func(_ context.Context, schema string) (string, error) {
			r.log = append(r.log, "owner "+schema)
			return "hm_p_1a2b3c4d_forms", nil
		},
	}
	return r
}

func TestRestoreStepsOrderAndArguments(t *testing.T) {
	r := newStepsRig(t, nil)
	if err := r.d.steps(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := "pg_restore,psql,provision forms,owner plugin_forms,pg_restore-l,pg_restore"
	if got := strings.Join(r.log, ","); got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	core := r.calls[0]
	if strings.Join(core[:5], " ") != "pg_restore --no-owner --role=hm_app --exit-on-error --dbname=site" || !strings.HasSuffix(core[5], "/core.dump") {
		t.Errorf("core call = %v", core)
	}
	psql := r.calls[1]
	if strings.Join(psql[:7], " ") != "psql -X -q -v ON_ERROR_STOP=1 -v app_role=hm_app" || psql[7] != "-f" {
		t.Errorf("psql call = %v", psql)
	}
	last := r.calls[len(r.calls)-1]
	if strings.Join(last[:4], " ") != "pg_restore --clean --if-exists --no-owner" || last[4] != "--role=hm_p_1a2b3c4d_forms" ||
		last[5] != "-L" || last[7] != "--dbname=site" || !strings.HasSuffix(last[8], "/plugin_forms.dump") {
		t.Errorf("plugin call = %v", last)
	}
	// The dumps reached the tools intact.
	if r.seen["dump core"] != "core" || r.seen["dump plugin_forms"] != "forms" {
		t.Errorf("dumps seen = %v", r.seen)
	}
}

func TestRestoreStepsCredentialsOnlyByEnv(t *testing.T) {
	r := newStepsRig(t, nil)
	if err := r.d.steps(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, c := range r.calls {
		for _, a := range c {
			if strings.Contains(a, "apppw") || strings.Contains(a, "adminpw") || strings.Contains(a, "://") {
				t.Errorf("secret or URL in args %v", c)
			}
		}
	}
	has := func(env []string, kv string) bool {
		for _, e := range env {
			if e == kv {
				return true
			}
		}
		return false
	}
	if !has(r.envs[0], "PGPASSWORD=apppw") || !has(r.envs[0], "PGUSER=hm_app") {
		t.Errorf("core env = %v", r.envs[0])
	}
	// The bootstrap runs as the superuser against the target database, not
	// the database the admin URL points at.
	boot := r.envs[1]
	if !has(boot, "PGUSER=su") || !has(boot, "PGPASSWORD=adminpw") || !has(boot, "PGDATABASE=site") || has(boot, "PGDATABASE=postgres") {
		t.Errorf("bootstrap env = %v", boot)
	}
}

func TestRestoreStepsBootstrapFileIs0600AndRemoved(t *testing.T) {
	r := newStepsRig(t, nil)
	var mode fs.FileMode
	run := r.d.run
	r.d.run = func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		if name == "psql" {
			fi, err := os.Stat(args[len(args)-1])
			if err != nil {
				t.Error(err)
			} else {
				mode = fi.Mode().Perm()
			}
		}
		return run(ctx, env, name, args...)
	}
	if err := r.d.steps(t.Context()); err != nil {
		t.Fatal(err)
	}
	if mode != 0o600 {
		t.Errorf("bootstrap file mode = %v", mode)
	}
	if r.seen["psql -f"] != bootstrapSQL {
		t.Error("psql did not get the embedded bootstrap SQL")
	}
	if _, err := os.Stat(r.paths["psql -f"]); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("bootstrap file left behind: %v", err)
	}
	if _, err := os.Stat(r.paths["workdir"]); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("work dir left behind: %v", err)
	}
}

func TestRestoreStepsListDropsSchemaLine(t *testing.T) {
	r := newStepsRig(t, nil)
	if err := r.d.steps(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := r.seen["pg_restore -L"]
	if strings.Contains(got, "SCHEMA - plugin_forms") || !strings.Contains(got, "TABLE plugin_forms deliveries") {
		t.Fatalf("list = %q", got)
	}
}

func TestRestoreStepsCoreFailureStopsEverything(t *testing.T) {
	r := newStepsRig(t, nil)
	r.fail["pg_restore"] = errors.New("exit status 1")
	err := r.d.steps(t.Context())
	if err == nil || !strings.Contains(err.Error(), "core database") || !strings.Contains(err.Error(), "pg_restore: error: boom") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "Command was") {
		t.Errorf("error repeats the command: %v", err)
	}
	if got := strings.Join(r.log, ","); got != "pg_restore" {
		t.Fatalf("ran %s after a core failure", got)
	}
}

func TestRestoreStepsBootstrapFailureStopsBeforePlugins(t *testing.T) {
	r := newStepsRig(t, nil)
	r.fail["psql"] = errors.New("exit status 3")
	err := r.d.steps(t.Context())
	if err == nil || !strings.Contains(err.Error(), "role bootstrap") {
		t.Fatalf("err = %v", err)
	}
	if got := strings.Join(r.log, ","); got != "pg_restore,psql" {
		t.Fatalf("ran %s", got)
	}
}

func TestRestoreStepsPluginFailureNamesPlugin(t *testing.T) {
	r := newStepsRig(t, nil)
	r.fail["provision"] = errors.New("role unavailable")
	err := r.d.steps(t.Context())
	if err == nil || !strings.Contains(err.Error(), "plugin forms") || !strings.Contains(err.Error(), "role unavailable") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(strings.Join(r.log, ","), "pg_restore-l") {
		t.Error("plugin dump listed after a failed provision")
	}
}

func TestRestoreStepsRefusesOddOwnerRole(t *testing.T) {
	r := newStepsRig(t, nil)
	r.d.ownerOf = func(context.Context, string) (string, error) { return "Bad Role", nil }
	if err := r.d.steps(t.Context()); err == nil || !strings.Contains(err.Error(), "no usable owner role") {
		t.Fatalf("err = %v", err)
	}
}

func TestRestoreStepsWithoutPlugins(t *testing.T) {
	r := newStepsRig(t, func(s *archSpec) {
		s.Parts = []string{coreName, contentName}
		s.Mf.Plugins = nil
	})
	if err := r.d.steps(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.log, ","); got != "pg_restore,psql" {
		t.Fatalf("ran %s", got)
	}
}

func TestRestoreStepsTargetFromURL(t *testing.T) {
	for url, ok := range map[string]bool{
		stepsAppURL:                      true,
		"postgres://h/site":              false, // no user
		"postgres://hm_app@h":            false, // no database
		"postgres://Bad-Role@h/site":     false,
		"postgres://hm_app:p@h/site?x=1": true,
		"postgres://h/site?user=hm_app":  true, // query user, as pgx reads it
	} {
		r := newStepsRig(t, nil)
		r.d.o.DatabaseURL = url
		err := r.d.parseTarget()
		if (err == nil) != ok {
			t.Errorf("%s: err = %v, want ok=%v", url, err, ok)
		}
	}
}

// The ambient PGDATABASE and PGUSER must not stand in for what the URL names.
func TestRestoreStepsIgnoresAmbientPGVars(t *testing.T) {
	t.Setenv("PGDATABASE", "x")
	t.Setenv("PGUSER", "y")
	for url, want := range map[string]string{
		"postgres://u@h":  "DATABASE_URL names no database",
		"postgres://u@h/": "DATABASE_URL names no database",
		"postgres://h/db": "DATABASE_URL needs the app role as its user",
	} {
		r := newStepsRig(t, nil)
		r.d.o.DatabaseURL = url
		if err := r.d.parseTarget(); err == nil || err.Error() != want {
			t.Errorf("%s: err = %v, want %q", url, err, want)
		}
	}
}

// pgx lets the query keys user and dbname win over the userinfo and the path.
// The checks connect through pgx, so the writes must go to the same role and
// database.
func TestRestoreStepsTargetQueryWins(t *testing.T) {
	r := newStepsRig(t, nil)
	r.d.o.DatabaseURL = "postgres://hm_app@h/site?dbname=scratch&user=hm_other"
	if err := r.d.parseTarget(); err != nil {
		t.Fatal(err)
	}
	if r.d.appRole != "hm_other" || r.d.dbName != "scratch" {
		t.Fatalf("role %q, database %q; want hm_other, scratch", r.d.appRole, r.d.dbName)
	}
}

func TestFilterSchemaLine(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"drops the schema entry":      {pluginListing, strings.Replace(pluginListing, "2615; 1259 1 SCHEMA - plugin_forms hm_p_1a2b3c4d_forms\n", "", 1)},
		"keeps a table named like it": {"1; 2 3 TABLE plugin_x t o\n", "1; 2 3 TABLE plugin_x t o\n"},
		"keeps other schemas":         {"1; 2 3 SCHEMA - public o\n", "1; 2 3 SCHEMA - public o\n"},
		"empty":                       {"", ""},
	}
	for name, c := range cases {
		if got := filterSchemaLine(c.in); got != c.want {
			t.Errorf("%s: got %q want %q", name, got, c.want)
		}
	}
}

func TestRestoreError(t *testing.T) {
	out := []byte("pg_restore: connecting\npg_restore: error: could not execute query: ERROR:  x\nCommand was: CREATE TABLE\n")
	if got := restoreError(out, errors.New("exit status 1")); got != "pg_restore: error: could not execute query: ERROR:  x" {
		t.Errorf("got %q", got)
	}
	if got := restoreError(nil, errors.New("exit status 1")); got != "exit status 1" {
		t.Errorf("got %q", got)
	}
}

// The guard must run before the bootstrap in the same psql session: it checks
// objects the dump controls, which the superuser bootstrap would then read.
func TestRestoreStepsGuardPrecedesBootstrapInOneSession(t *testing.T) {
	r := newStepsRig(t, nil)
	if err := r.d.steps(t.Context()); err != nil {
		t.Fatal(err)
	}
	var psql []string
	n := 0
	for _, c := range r.calls {
		if c[0] == "psql" {
			psql = c
			n++
		}
	}
	if n != 1 {
		t.Fatalf("psql ran %d times, want one session", n)
	}
	var files []string
	for i, a := range psql {
		if a == "-f" {
			files = append(files, psql[i+1])
		}
	}
	if len(files) != 2 || !strings.Contains(filepath.Base(files[0]), "guard") || !strings.Contains(filepath.Base(files[1]), "bootstrap") {
		t.Fatalf("psql files = %v", files)
	}
}

func TestBootstrapGuardSQLChecks(t *testing.T) {
	for _, want := range []string{"search_path = pg_catalog, pg_temp", "hatamoto_admin", "<> 'r'", "pg_trigger", "pg_rewrite", "pg_catalog.text"} {
		if !strings.Contains(bootstrapGuardSQL, want) {
			t.Errorf("guard lacks %q", want)
		}
	}
}

// Provision succeeds when the plugin role cannot be created; the restore must
// name that reason instead of failing later on an unusable owner.
func TestProvisionCheckedReturnsTheRoleError(t *testing.T) {
	reason := errors.New("plugin role unavailable: run deploy/sql/role-admin-bootstrap.sql")
	provision := func(context.Context, string) error { return nil }
	err := provisionChecked(t.Context(), "forms", provision, func(string) error { return reason })
	if !errors.Is(err, reason) {
		t.Fatalf("err = %v, want the role error", err)
	}
	if err := provisionChecked(t.Context(), "forms", provision, func(string) error { return nil }); err != nil {
		t.Fatalf("usable role: %v", err)
	}
	boom := errors.New("boom")
	err = provisionChecked(t.Context(), "forms", func(context.Context, string) error { return boom },
		func(string) error { t.Error("role error read after a failed provision"); return nil })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}
