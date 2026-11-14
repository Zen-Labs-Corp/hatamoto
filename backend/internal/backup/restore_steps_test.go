package backup

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
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
2037; 826 3 DEFAULT ACL - DEFAULT PRIVILEGES FOR FUNCTIONS hm_source_app
`

// stepsCoreListing is the fake core dump listing: the source app role owns
// the instance table; ACL entries carry no grantees.
const stepsCoreListing = `;
2000; 1259 9 TABLE public hatamoto_instance hm_source_app
2001; 0 0 ACL public TABLE hatamoto_instance hm_source_app
2002; 826 8 DEFAULT ACL public DEFAULT PRIVILEGES FOR TABLES hm_source_app
2037; 826 3 DEFAULT ACL - DEFAULT PRIVILEGES FOR FUNCTIONS hm_source_app
1259; 1259 2 TABLE plugin_forms deliveries hm_p_1a2b3c4d_forms
`

// analysisLog is what the role analysis runs for a dump: its listing, then
// its grants.
const analysisLog = "pg_restore-l,pg_restore-acl"

// fullAnalysis is the analysis of the core dump and the one plugin dump.
const fullAnalysis = analysisLog + "," + analysisLog

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

	pauseSQL []string          // the SQL the off-site pause ran as the app role
	aclSQL   map[string]string // dump file -> what pg_restore prints for its grants
	scripts  []string          // the grant scripts psql was given, at call time
}

func newStepsRig(t *testing.T, mut func(*archSpec)) *stepsRig {
	t.Helper()
	arch := buildArchive(t, mut)
	sc, err := scanArchive(arch)
	if err != nil {
		t.Fatal(err)
	}
	r := &stepsRig{seen: map[string]string{}, paths: map[string]string{}, fail: map[string]error{}, aclSQL: map[string]string{
		coreName:            "GRANT SELECT ON TABLE public.hatamoto_instance TO hm_source_app;\nGRANT SELECT ON TABLE public.hatamoto_instance TO gone;\n",
		"plugin_forms.dump": "GRANT SELECT ON TABLE plugin_forms.deliveries TO hm_source_app;\n",
	}}
	rr := &restorer{
		o:   RestoreOptions{DatabaseURL: stepsAppURL},
		req: RestoreRequest{AdminURL: stepsAdminURL},
		src: arch, scan: sc, mf: sc.mf,
		log: slog.New(slog.DiscardHandler),
	}
	rr.o.Q = &engineQuerier{}
	rr.o.PauseOffsite = func(_ context.Context, sql string) error {
		r.log = append(r.log, "pause offsite")
		r.pauseSQL = append(r.pauseSQL, sql)
		return r.fail["pause offsite"]
	}
	rr.run = func(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
		r.calls = append(r.calls, append([]string{name}, args...))
		r.envs = append(r.envs, env)
		label := name
		if name == "pg_restore" && len(args) > 0 && args[0] == "-l" {
			label = "pg_restore-l"
		}
		if name == "pg_restore" && slices.Contains(args, "-f") {
			label = "pg_restore-acl"
		}
		r.log = append(r.log, label)
		// Remember what the files named in the args hold right now.
		for i, a := range args {
			if (a == "-f" || a == "-L") && i+1 < len(args) && args[i+1] != "-" {
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
			if strings.HasSuffix(last, "/"+coreName) {
				r.seen["core -L"] = r.seen["pg_restore -L"]
			}
		}
		if err, ok := r.fail[label]; ok {
			return []byte("pg_restore: error: boom\nCommand was: x\n"), err
		}
		if label == "pg_restore-l" && strings.HasSuffix(args[len(args)-1], "/"+coreName) {
			return []byte(stepsCoreListing), nil
		}
		if label == "pg_restore-l" {
			return []byte(pluginListing), nil
		}
		if label == "pg_restore-acl" {
			return []byte(r.aclSQL[filepath.Base(args[len(args)-1])]), nil
		}
		return nil, nil
	}
	r.d = &dbRestorer{restorer: rr,
		grants: func(_ context.Context, scripts []grantScript) error {
			r.log = append(r.log, "grants")
			for _, sc := range scripts {
				prefix := ""
				if sc.role != "" {
					prefix = "SET ROLE " + sc.role + ";\n"
				}
				r.scripts = append(r.scripts, prefix+strings.Join(sc.stmts, "\n")+"\n")
			}
			return r.fail["grants"]
		},
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
	want := fullAnalysis + ",pg_restore-l,pg_restore,psql,pause offsite,provision forms,owner plugin_forms,pg_restore-l,pg_restore,grants"
	if got := strings.Join(r.log, ","); got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	core := r.calls[5]
	if strings.Join(core[:5], " ") != "pg_restore --no-owner --role=hm_app --exit-on-error --single-transaction" || core[5] != "-L" ||
		core[7] != "--dbname=site" || !strings.HasSuffix(core[8], "/core.dump") {
		t.Errorf("core call = %v", core)
	}
	if got := r.seen["core -L"]; strings.Contains(got, "ACL") || !strings.Contains(got, "TABLE plugin_forms") {
		t.Errorf("core list = %q", got)
	}
	psql := r.calls[6]
	if strings.Join(psql[:7], " ") != "psql -X -q -v ON_ERROR_STOP=1 -v app_role=hm_app" || psql[7] != "-f" {
		t.Errorf("psql call = %v", psql)
	}
	last := r.calls[len(r.calls)-1]
	if strings.Join(last[:4], " ") != "pg_restore --clean --if-exists --no-owner" || last[4] != "--role=hm_p_1a2b3c4d_forms" ||
		last[5] != "--single-transaction" || last[6] != "-L" || last[8] != "--dbname=site" || !strings.HasSuffix(last[9], "/plugin_forms.dump") {
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
	if !has(r.envs[5], "PGPASSWORD=apppw") || !has(r.envs[5], "PGUSER=hm_app") {
		t.Errorf("core env = %v", r.envs[5])
	}
	// The bootstrap runs as the superuser against the target database, not
	// the database the admin URL points at.
	boot := r.envs[6]
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
			fi, err := os.Stat(args[len(args)-1]) // the last -f file
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

// The admin role of a managed database has no privilege on the app role's
// tables, so the off-site pause runs as the app role, once, after the bootstrap.
func TestRestoreStepsPauseOffsiteRunsAsAppRoleAfterBootstrap(t *testing.T) {
	r := newStepsRig(t, nil)
	if err := r.d.steps(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.pauseSQL) != 1 || r.pauseSQL[0] != pauseOffsiteSQL {
		t.Fatalf("pause ran %q, want %q once", r.pauseSQL, pauseOffsiteSQL)
	}
	for _, c := range r.calls {
		for _, a := range c {
			if a == "-c" || a == pauseOffsiteSQL {
				t.Errorf("a tool call carries the pause SQL: %v", c)
			}
		}
	}
	if got := strings.Join(r.log[:8], ","); got != fullAnalysis+",pg_restore-l,pg_restore,psql,pause offsite" {
		t.Errorf("pause not right after the bootstrap: %s", strings.Join(r.log, ","))
	}
}

func TestRestoreStepsPauseOffsiteFailureAborts(t *testing.T) {
	r := newStepsRig(t, nil)
	r.fail["pause offsite"] = errors.New("permission denied")
	err := r.d.steps(t.Context())
	if err == nil || !strings.Contains(err.Error(), "off-site backup pause") || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err = %v", err)
	}
	if got := strings.Join(r.log, ","); got != fullAnalysis+",pg_restore-l,pg_restore,psql,pause offsite" {
		t.Fatalf("ran %s after a failed pause", got)
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
	if got := strings.Join(r.log, ","); got != fullAnalysis+",pg_restore-l,pg_restore" {
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
	if got := strings.Join(r.log, ","); got != fullAnalysis+",pg_restore-l,pg_restore,psql" {
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
	if strings.Count(strings.Join(r.log, ","), "pg_restore-l") != 3 { // both analysed, the core restored
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
	if got := strings.Join(r.log, ","); got != analysisLog+",pg_restore-l,pg_restore,psql,pause offsite,grants" {
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

func TestFilterDefaultACL(t *testing.T) {
	in := "2037; 826 20714 DEFAULT ACL - DEFAULT PRIVILEGES FOR FUNCTIONS src_app\n215; 1259 20715 TABLE public t src_app\n"
	want := "215; 1259 20715 TABLE public t src_app\n"
	if got := filterDefaultACL(in); got != want {
		t.Errorf("got %q want %q", got, want)
	}
	keep := "1; 2 3 TABLE public default_acl_t o\n"
	if got := filterDefaultACL(keep); got != keep {
		t.Errorf("kept entry changed: %q", got)
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
		if c[0] == "psql" && !slices.Contains(c, "--single-transaction") {
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

// The restore lists leave out every ACL entry; the grants come back mapped to
// the target app role, once the plugin roles exist, and a grant to a role
// that is not here is dropped.
func TestRestoreStepsGrantScripts(t *testing.T) {
	r := newStepsRig(t, nil)
	if err := r.d.steps(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := r.seen["pg_restore -L"]; strings.Contains(got, "ACL") {
		t.Errorf("plugin list keeps an ACL entry: %q", got)
	}
	if len(r.scripts) != 2 {
		t.Fatalf("%d grant scripts, want core and plugin: %q", len(r.scripts), r.scripts)
	}
	wantCore := "GRANT SELECT ON TABLE public.hatamoto_instance TO hm_app;\n"
	if r.scripts[0] != wantCore {
		t.Errorf("core script = %q, want %q", r.scripts[0], wantCore)
	}
	wantPlugin := "SET ROLE hm_p_1a2b3c4d_forms;\nGRANT SELECT ON TABLE plugin_forms.deliveries TO hm_app;\n"
	if r.scripts[1] != wantPlugin {
		t.Errorf("plugin script = %q, want %q", r.scripts[1], wantPlugin)
	}
	for _, s := range r.scripts {
		if strings.Contains(s, "hm_source_app") || strings.Contains(s, "gone") {
			t.Errorf("script names a source or absent role: %q", s)
		}
	}
	if got := skipWarnings(&roleAnalysis{Skipped: []string{"gone"}}); len(got) != 1 || got[0] != "grants to role gone are skipped (no such role here)" {
		t.Errorf("skipWarnings = %q", got)
	}
}

func TestRestoreStepsGrantScriptFailureStopsTheRestore(t *testing.T) {
	r := newStepsRig(t, nil)
	r.fail["grants"] = errors.New("boom")
	err := r.d.steps(t.Context())
	if err == nil || !strings.Contains(err.Error(), "restore stopped at grants: boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestRestoreStepsLedger(t *testing.T) {
	r := newStepsRig(t, nil)
	if err := r.d.steps(t.Context()); err != nil {
		t.Fatal(err)
	}
	if l := r.d.led; !l.coreDone || !l.bootstrapStarted || len(l.plugins) != 1 || l.plugins[0] != "forms" {
		t.Errorf("ledger after a full run = %+v", l)
	}

	r = newStepsRig(t, nil)
	r.fail["pg_restore"] = errors.New("exit status 1")
	_ = r.d.steps(t.Context())
	if l := r.d.led; l.coreDone || l.bootstrapStarted {
		t.Errorf("ledger after a core failure = %+v", l)
	}

	r = newStepsRig(t, nil)
	r.fail["psql"] = errors.New("exit status 3")
	_ = r.d.steps(t.Context())
	if l := r.d.led; !l.coreDone || !l.bootstrapStarted || len(l.plugins) != 0 {
		t.Errorf("ledger after a bootstrap failure = %+v", l)
	}

	r = newStepsRig(t, nil)
	r.fail["provision"] = errors.New("role unavailable")
	_ = r.d.steps(t.Context())
	if l := r.d.led; len(l.plugins) != 1 {
		t.Errorf("a plugin whose provisioning failed must be in the ledger: %+v", l)
	}
}
