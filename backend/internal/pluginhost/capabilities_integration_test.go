//go:build integration

package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/core/migrations"
	"hatamoto.dev/sdk"
)

// These tests need a Postgres. The application role they run as is never a
// superuser: given a superuser URL they create the plain role hm_it_app
// (CREATEROLE, owns the scratch database, like production plus the one
// privilege plugin roles need); given a non-superuser URL that may create
// databases and roles they use it directly:
//
//	DATABASE_URL=postgres://hm_app:x@127.0.0.1:55981/postgres go test -tags=integration ./internal/pluginhost
//
// Each test gets its own database, migrated with the real core migrations.

// roleMode is how the application role may create plugin roles in a test.
type roleMode int

const (
	modeCreateRole roleMode = iota // direct CREATE ROLE: the app role has CREATEROLE
	modeBootstrap                  // the app role has no extra attribute; the bootstrap SQL ran as superuser
)

func (m roleMode) String() string { return [...]string{"createrole", "bootstrap"}[m] }

func scratchDB(t *testing.T) (*pgxpool.Pool, string) {
	return scratchDBMode(t, modeCreateRole)
}

// runBootstrap runs deploy/sql/role-admin-bootstrap.sql with psql as the
// superuser behind cfg, in database db, for application role app.
func runBootstrap(t *testing.T, super *pgx.ConnConfig, db, app string) {
	t.Helper()
	psql, err := exec.LookPath("psql")
	if err != nil {
		t.Skip("psql not installed")
	}
	cmd := exec.Command(psql, "-X", "-q", "-v", "ON_ERROR_STOP=1", "-v", "app_role="+app, "-d", db,
		"-f", "../../../deploy/sql/role-admin-bootstrap.sql")
	cmd.Env = append(os.Environ(), "PGHOST="+super.Host, "PGPORT="+strconv.Itoa(int(super.Port)),
		"PGUSER="+super.User, "PGPASSWORD="+super.Password)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap: %v\n%s", err, out)
	}
}

func scratchDBMode(t *testing.T, mode roleMode) (*pgxpool.Pool, string) {
	return scratchDBRole(t, mode, "")
}

// scratchDBRole is scratchDBMode with a chosen application role (given a
// superuser URL), so two databases of one cluster can have different owners.
func scratchDBRole(t *testing.T, mode roleMode, role string) (*pgxpool.Pool, string) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	var super bool
	if err := admin.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_pl_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	owner := ""
	if mode == modeBootstrap && !super {
		t.Skip("the bootstrap mode needs a superuser DATABASE_URL")
	}
	appRole := "hm_it_app"
	if mode == modeBootstrap {
		appRole = "hm_it_lowapp"
	}
	if role != "" {
		appRole = role
	}
	if super {
		// A superuser would hide every privilege bug, so the application
		// runs as a plain role that mirrors production: owns its database,
		// may create roles, is not a superuser.
		var exists bool
		_ = admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, appRole).Scan(&exists)
		if !exists {
			attr := "CREATEROLE"
			if mode == modeBootstrap {
				attr = "NOCREATEROLE"
			}
			if _, err := admin.Exec(ctx, `CREATE ROLE `+appRole+` LOGIN PASSWORD 'x' `+attr); err != nil {
				t.Fatal(err)
			}
		}
		owner = " OWNER " + appRole
		cfg.ConnConfig.User, cfg.ConnConfig.Password = appRole, "x"
	}
	superCfg := admin.Config().Copy()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name+owner); err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	if mode == modeBootstrap {
		runBootstrap(t, superCfg, name, appRole)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var dbID string // set once the core migrations ran
	t.Cleanup(func() {
		pool.Close()
		// Plugin roles are cluster-wide: drop what this database made.
		var roles []string
		if dbID != "" {
			rows, _ := admin.Query(ctx, `SELECT rolname FROM pg_roles WHERE rolname LIKE $1 OR rolname = 'hm_plain'`, "hm\\_p\\_"+dbID[:8]+"\\_%")
			for rows != nil && rows.Next() {
				var r string
				_ = rows.Scan(&r)
				roles = append(roles, r)
			}
			if rows != nil {
				rows.Close()
			}
		}
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		for _, r := range roles {
			_, _ = admin.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{r}.Sanitize())
		}
		if mode == modeBootstrap && dbID != "" {
			dropRoleAdmin(ctx, admin, dbID)
		}
		if mode == modeBootstrap || role != "" {
			_, _ = admin.Exec(ctx, "DROP ROLE IF EXISTS "+appRole)
		}
		admin.Close(ctx)
	})

	db := stdlib.OpenDBFromPool(pool)
	prov, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prov.Up(ctx); err != nil {
		t.Fatalf("core migrations: %v", err)
	}
	if dbID, err = instanceID(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_users (id, email, password_hash, role) VALUES (gen_random_uuid(), 'secret@example.com', 'x', 'owner')`); err != nil {
		t.Fatalf("seeding a user: %v", err)
	}
	return pool, name
}

// dropRoleAdmin removes a database's hatamoto_role_admin_<id8>, which holds a
// parameter grant that would otherwise make DROP ROLE fail.
func dropRoleAdmin(ctx context.Context, admin *pgx.Conn, dbID string) {
	r := pgx.Identifier{"hatamoto_role_admin_" + dbID[:8]}.Sanitize()
	_, _ = admin.Exec(ctx, "REVOKE SET ON PARAMETER temp_file_limit FROM "+r)
	_, _ = admin.Exec(ctx, "DROP ROLE IF EXISTS "+r)
}

// roleOf is the Postgres role of plugin in r's database.
func (r *pgRig) roleOf(t *testing.T, plugin string) string {
	t.Helper()
	id, err := instanceID(context.Background(), r.pool)
	if err != nil {
		t.Fatal(err)
	}
	return roleNameFor(id, plugin)
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type pgRig struct {
	*capaRig
	pool   *pgxpool.Pool
	db     *PluginDB
	grants *GrantStore
}

func newPGRig(t *testing.T) *pgRig { return newPGRigMode(t, modeCreateRole) }

func newPGRigMode(t *testing.T, mode roleMode) *pgRig {
	t.Helper()
	pool, _ := scratchDBMode(t, mode)
	r := newCapaRig(t)
	db := NewPluginDB(pool, quiet())
	grants := NewGrantStore(pool)
	svc := r.rt.caps.api.svc
	svc.DB = db
	r.rt.Configure(Config{Services: svc, Grants: grants, Scheduler: r.sched})
	return &pgRig{capaRig: r, pool: pool, db: db, grants: grants}
}

func (r *pgRig) query(t *testing.T, sql string, args ...any) outcome {
	t.Helper()
	return r.do(t, sdk.FnDBQuery, map[string]any{"sql": sql, "args": args})
}

func TestPluginCannotReadCoreTables(t *testing.T) {
	for _, mode := range []roleMode{modeCreateRole, modeBootstrap} {
		t.Run(mode.String(), func(t *testing.T) { pluginCannotReadCoreTables(t, mode) })
	}
}

func pluginCannotReadCoreTables(t *testing.T, mode roleMode) {
	r := newPGRigMode(t, mode)
	r.load(t, nil)
	r.grant(t, sdk.PermDB, sdk.PermKV)

	var users int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM hatamoto_users`).Scan(&users); err != nil || users == 0 {
		t.Fatalf("the app user must see a user row to make this test meaningful: %d %v", users, err)
	}

	// The legitimate path works.
	if o := r.do(t, sdk.FnDBExec, map[string]any{"sql": `CREATE TABLE t (id int)`}); o.Code != sdk.ErrBlocked {
		t.Fatalf("DDL through db.exec must be refused: %+v", o)
	}
	if o := r.query(t, `SELECT 1 AS one, current_user AS u`); o.Code != "" {
		t.Fatalf("a plain select: %+v", o)
	}

	attacks := map[string]string{
		"core table":             `SELECT * FROM public.hatamoto_users`,
		"core table unqualified": `SELECT * FROM hatamoto_users`,
		"pg_catalog user table":  `SELECT * FROM pg_catalog.pg_authid`,
		"set role":               `SET ROLE app_user`,
		"set local role":         `SET LOCAL ROLE postgres`,
		"reset role":             `RESET ROLE`,
		"set session auth":       `SET SESSION AUTHORIZATION DEFAULT`,
		"set search_path":        `SET search_path = public`,
		"multi-statement":        `SELECT 1; SELECT * FROM public.hatamoto_users`,
		"multi reset":            `SELECT 1; RESET ROLE; SELECT * FROM hatamoto_users`,
		"copy":                   `COPY public.hatamoto_users TO STDOUT`,
		"copy program":           `COPY (SELECT 1) TO PROGRAM 'id'`,
		"pg_read_file":           `SELECT pg_read_file('/etc/passwd')`,
		"pg_ls_dir":              `SELECT pg_ls_dir('/')`,
		"dblink":                 `SELECT * FROM dblink('dbname=postgres', 'select 1') AS t(a int)`,
		"current_setting":        `SELECT current_setting('role')`,
		"current_setting user":   `select current_setting('session_authorization')`,
		"set_config role":        `SELECT set_config('role', current_setting('session_authorization'), true)`,
		"set_config alone":       `SELECT set_config('search_path', 'public', true)`,
		"create function":        `CREATE FUNCTION f() RETURNS int LANGUAGE sql SET role = 'app_user' AS 'select 1'`,
		"do block":               `DO $$ BEGIN PERFORM 1; END $$`,
		"other plugin schema":    `SELECT * FROM plugin_other.kv`,
		"core kv table":          `SELECT * FROM hatamoto_plugin_kv`,
		"credentials":            `SELECT * FROM hatamoto_plugin_credentials`,
		"information_schema":     `SELECT * FROM information_schema.tables`,
		"lo_import":              `SELECT lo_import('/etc/passwd')`,
		"unicode escape":         `SELECT U&"set\005fconfig"('role','app_user',true)`,
	}
	for name, sql := range attacks {
		o := r.query(t, sql)
		if o.Code == "" {
			t.Errorf("%s: %q succeeded: %s", name, sql, o.Result)
			continue
		}
		if o.Code != sdk.ErrBlocked && o.Code != sdk.ErrInvalidArgument {
			t.Errorf("%s: code %q (%s), want blocked", name, o.Code, o.Msg)
		}
		if strings.Contains(string(o.Result), "secret@example.com") {
			t.Errorf("%s leaked data", name)
		}
	}

	// The guard is only defence in depth: TestPluginEscapesHoldWithoutTheGuard
	// runs this table again with the guard switched off.
}

func TestDBQueryExecAndLimits(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	if err := r.db.Provision(ctx, "capa"); err != nil {
		t.Fatal(err)
	}
	migs, err := ReadMigrations(fsOf(map[string]string{
		"migrations/001_init.sql": "-- +goose Up\nCREATE TABLE leads (id serial PRIMARY KEY, email text NOT NULL, meta jsonb, at timestamptz DEFAULT now(), uid uuid DEFAULT gen_random_uuid());\nCREATE INDEX ON leads (email);\n-- +goose Down\nDROP TABLE leads;\n",
	}), "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.db.Migrate(ctx, "capa", migs); err != nil || n != 1 {
		t.Fatalf("migrate: %d %v", n, err)
	}
	if n, err := r.db.Migrate(ctx, "capa", migs); err != nil || n != 0 {
		t.Fatalf("a second run must do nothing: %d %v", n, err)
	}

	// The version table lives in the plugin's own schema, owned by its role.
	if res, err := r.db.Query(ctx, "capa", `SELECT count(*) FROM goose_db_version WHERE version_id = 1`); err != nil || res.Rows[0][0] != int64(1) {
		t.Fatalf("version table: %+v %v", res, err)
	}

	n, err := r.db.Exec(ctx, "capa", `INSERT INTO leads (email, meta) VALUES ($1, $2), ($3, $4)`,
		[]any{"a@example.com", `{"x":1}`, "b@example.com", nil}...)
	if err != nil || n != 2 {
		t.Fatalf("insert: %d %v", n, err)
	}
	res, err := r.db.Query(ctx, "capa", `SELECT id, email, meta, at, uid FROM leads WHERE email = $1`, "a@example.com")
	if err != nil || len(res.Rows) != 1 || res.Rows[0][1] != "a@example.com" {
		t.Fatalf("query: %+v %v", res, err)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("a result must encode: %v", err)
	}
	if !strings.Contains(string(b), `"uid":`) && !strings.Contains(string(b), `,"`) {
		t.Fatalf("result json: %s", b)
	}
	args, err := DecodeArgs(json.RawMessage(`[7, "x", 1.5, true, null, {"a":1}, [1,2]]`))
	if err != nil || len(args) != 7 {
		t.Fatalf("args: %v %v", args, err)
	}
	if res, err := r.db.Query(ctx, "capa", `SELECT $1::int, $2::text, $3::float8, $4::bool, $5::text, $6::jsonb, $7::jsonb`, args...); err != nil || len(res.Rows) != 1 {
		t.Fatalf("typed args: %+v %v", res, err)
	}

	// Row cap.
	res, err = r.db.Query(ctx, "capa", `SELECT g FROM generate_series(1, 5000) g`)
	if err != nil || !res.Truncated || len(res.Rows) != maxDBRows {
		t.Fatalf("row cap: truncated=%v rows=%d err=%v", res != nil && res.Truncated, len(res.Rows), err)
	}

	// Statement timeout bounds runaway SQL.
	short, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = r.db.Query(short, "capa", `SELECT count(*) FROM generate_series(1, 2000000000)`)
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("runaway query: %v after %s", err, time.Since(start))
	}
	// ... and leaves the pool usable.
	if _, err := r.db.Query(ctx, "capa", `SELECT 1`); err != nil {
		t.Fatalf("after timeout: %v", err)
	}

	// A migration that reaches outside the schema fails and records nothing.
	bad, err := ReadMigrations(fsOf(map[string]string{
		"migrations/002_bad.sql": "CREATE TABLE ok2 (id int);\nCREATE TABLE x AS SELECT * FROM public.hatamoto_users;",
	}), "migrations")
	if err == nil {
		// public is a banned word, so ReadMigrations refuses first.
		t.Fatalf("a migration naming public must be refused up front: %+v", bad)
	}
	viaFK := []Migration{{Version: 3, Name: "3_fk.sql", Stmts: []string{
		`CREATE TABLE ok3 (id int)`, `CREATE TABLE fk3 (u uuid REFERENCES hatamoto_users (id))`}}}
	if _, err := r.db.Migrate(ctx, "capa", viaFK); err == nil {
		t.Fatal("a foreign key into a core table must fail: the role has no REFERENCES right")
	}
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = 'plugin_capa' AND tablename = 'ok3')`).Scan(&exists); err != nil || exists {
		t.Fatalf("a failed migration must roll back (ok3 exists=%v %v)", exists, err)
	}
}

func TestPluginsAreIsolatedFromEachOther(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	for _, p := range []string{"alpha", "beta"} {
		if err := r.db.Provision(ctx, p); err != nil {
			t.Fatal(err)
		}
		if _, err := r.db.Migrate(ctx, p, []Migration{{Version: 1, Name: "1.sql", Stmts: []string{`CREATE TABLE notes (v text)`, `INSERT INTO notes VALUES ('` + p + `')`}}}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := r.db.Query(ctx, "alpha", `SELECT v FROM notes`)
	if err != nil || len(res.Rows) != 1 || res.Rows[0][0] != "alpha" {
		t.Fatalf("alpha sees %+v %v", res, err)
	}
	if _, err := r.db.Query(ctx, "alpha", `SELECT v FROM plugin_beta.notes`); err == nil {
		t.Fatal("alpha read beta's schema")
	}
	// Even bypassing the guard, alpha's role has no right on beta's schema.
	r.db.guard = unguarded
	if _, err := r.db.Query(ctx, "alpha", `SELECT v FROM plugin_beta.notes`); err == nil {
		t.Fatal("the database let alpha's role into beta's schema")
	}
}

func TestKV(t *testing.T) {
	r := newPGRig(t)
	r.load(t, nil)
	r.grant(t, sdk.PermKV)

	if o := r.do(t, sdk.FnKVGet, map[string]any{"key": "a"}); o.Code != "" || string(o.Result) != `{"found":false}` {
		t.Fatalf("get missing: %+v", o)
	}
	if o := r.do(t, sdk.FnKVSet, map[string]any{"key": "a", "value": map[string]any{"n": 1}}); o.Code != "" {
		t.Fatalf("set: %+v", o)
	}
	if o := r.do(t, sdk.FnKVGet, map[string]any{"key": "a"}); o.Code != "" || !strings.Contains(string(o.Result), `"n": 1`) && !strings.Contains(string(o.Result), `"n":1`) {
		t.Fatalf("get: %+v", o)
	}
	r.do(t, sdk.FnKVSet, map[string]any{"key": "b/1", "value": 1})
	r.do(t, sdk.FnKVSet, map[string]any{"key": "b/2", "value": 2})
	if o := r.do(t, sdk.FnKVList, map[string]any{"prefix": "b/"}); !strings.Contains(string(o.Result), `"b/1"`) || strings.Contains(string(o.Result), `"a"`) {
		t.Fatalf("list: %+v", o)
	}
	r.do(t, sdk.FnKVSet, map[string]any{"key": "tmp", "value": true, "ttl_seconds": 1})
	time.Sleep(1200 * time.Millisecond)
	if o := r.do(t, sdk.FnKVGet, map[string]any{"key": "tmp"}); string(o.Result) != `{"found":false}` {
		t.Fatalf("an expired key is still served: %+v", o)
	}
	if o := r.do(t, sdk.FnKVDelete, map[string]any{"key": "a"}); o.Code != "" {
		t.Fatalf("delete: %+v", o)
	}
	if o := r.do(t, sdk.FnKVGet, map[string]any{"key": "a"}); string(o.Result) != `{"found":false}` {
		t.Fatalf("deleted key: %+v", o)
	}
	for name, args := range map[string]any{
		"empty key":    map[string]any{"key": "", "value": 1},
		"long key":     map[string]any{"key": strings.Repeat("k", maxKVKeyBytes+1), "value": 1},
		"big value":    map[string]any{"key": "k", "value": strings.Repeat("v", maxKVValueBytes+1)},
		"no value":     map[string]any{"key": "k"},
		"negative ttl": map[string]any{"key": "k", "value": 1, "ttl_seconds": -1},
	} {
		if o := r.do(t, sdk.FnKVSet, args); o.Code != sdk.ErrInvalidArgument {
			t.Errorf("%s: %+v", name, o)
		}
	}
	// kv is per plugin: another plugin's namespace never sees the key.
	if err := r.db.Provision(context.Background(), "other"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := r.db.KVGet(context.Background(), "other", "b/1"); found {
		t.Fatal("kv leaked across plugins")
	}
}

func TestCronAndFormSubmittedReachKV(t *testing.T) {
	r := newPGRig(t)
	r.load(t, nil)
	r.grant(t, sdk.PermKV, sdk.PermFormsRead)

	r.sched.mu.Lock()
	fn := r.sched.fns["plugin:capa:tick"]
	r.sched.mu.Unlock()
	if err := fn(context.Background()); err != nil {
		t.Fatalf("cron: %v", err)
	}
	if o := r.do(t, sdk.FnKVGet, map[string]any{"key": "cron:tick"}); !strings.Contains(string(o.Result), `"found":true`) {
		t.Fatalf("the cron export did not store its marker: %+v", o)
	}

	r.engine.Fire(context.Background(), sdk.FormSubmitted.Name, sdk.FormSubmittedEvent{
		FormSlug: "contact", Fields: map[string]any{"email": "lead@example.com"}})
	r.engine.Wait()
	o := r.do(t, sdk.FnKVGet, map[string]any{"key": "form:contact"})
	if !strings.Contains(string(o.Result), "lead@example.com") {
		t.Fatalf("form.submitted did not reach the plugin: %+v\n%s", o, r.logs.String())
	}
}

func TestGrantsPersist(t *testing.T) {
	pool, _ := scratchDB(t)
	ctx := context.Background()
	g := NewGrantStore(pool)
	if err := g.Set(ctx, "p", []string{sdk.PermKV, sdk.PermDB, sdk.PermKV}); err != nil {
		t.Fatal(err)
	}
	g2 := NewGrantStore(pool)
	if g2.Has("p", sdk.PermKV) {
		t.Fatal("a fresh store must read nothing before Load")
	}
	if err := g2.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if got := g2.Granted("p"); len(got) != 2 || !g2.Has("p", sdk.PermDB) || g2.Has("p", sdk.PermMailSend) {
		t.Fatalf("granted = %v", got)
	}
	if err := g2.Set(ctx, "p", []string{"bogus"}); err == nil {
		t.Fatal("unknown permission accepted")
	}
}

// With the bootstrap (no CREATEROLE on the application role) plugin roles come
// from the SECURITY DEFINER functions, and those functions are not a way out.
func TestBootstrapProvisioning(t *testing.T) {
	r := newPGRigMode(t, modeBootstrap)
	ctx := context.Background()
	var attrs int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM pg_roles WHERE rolname = current_user AND (rolsuper OR rolcreaterole OR rolcreatedb)`).Scan(&attrs); err != nil || attrs != 0 {
		t.Fatalf("the application role must have no privilege attribute: %d %v", attrs, err)
	}
	if _, err := r.pool.Exec(ctx, `CREATE ROLE hm_p_direct NOLOGIN`); err == nil {
		t.Fatal("the application role created a role directly")
	}
	if err := r.db.Provision(ctx, "viafunc"); err != nil {
		t.Fatal(err)
	}
	if err := r.db.RoleError("viafunc"); err != nil {
		t.Fatalf("role error through the function: %v", err)
	}
	if _, err := r.db.Exec(ctx, "viafunc", `SELECT 1`); err != nil {
		t.Fatalf("db through the function-made role: %v", err)
	}
	// Idempotent, and reprovisioning works.
	if err := r.db.Reprovision(ctx, "viafunc"); err != nil || r.db.RoleError("viafunc") != nil {
		t.Fatalf("reprovision: %v / %v", err, r.db.RoleError("viafunc"))
	}

	// The function accepts nothing but plain plugin names.
	for _, bad := range []string{"", "X", "1a", "a-b", "a b", "a;b", `a"b`, "a'b", "a\nb", "a\n", strings.Repeat("a", 42), "é"} {
		if _, err := r.pool.Exec(ctx, `SELECT hatamoto_admin.provision_plugin_role($1, 'SCRAM-SHA-256$4096:a$b:c')`, bad); err == nil {
			t.Errorf("provision accepted %q", bad)
		}
		if _, err := r.pool.Exec(ctx, `SELECT hatamoto_admin.drop_plugin_role($1)`, bad); err == nil {
			t.Errorf("drop accepted %q", bad)
		}
	}
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM pg_roles WHERE rolname LIKE 'hm\_p\_' || substr($1, 6, 8) || '\_%' AND rolname <> $1`, r.roleOf(t, "viafunc")).Scan(&n); err != nil || n != 0 {
		t.Fatalf("stray plugin roles after rejected names: %d %v", n, err)
	}
	// It never touches a role that is not a plain plugin role.
	if _, err := r.pool.Exec(ctx, `SELECT hatamoto_admin.drop_plugin_role('x')`); err != nil {
		t.Fatalf("dropping a missing role must be a no-op: %v", err)
	}
	if _, err := r.pool.Exec(ctx, `SET ROLE hatamoto_role_admin_`+r.roleOf(t, "x")[5:13]); err == nil {
		t.Fatal("the application role can become hatamoto_role_admin")
	}

	// The plugin's own role cannot reach the functions or their schema.
	r.db.guard = unguarded
	for _, q := range []string{
		`SELECT hatamoto_admin.provision_plugin_role('other', 'SCRAM-SHA-256$4096:a$b:c')`,
		`SELECT hatamoto_admin.drop_plugin_role('viafunc')`,
		`SELECT hatamoto_admin.app_role()`,
	} {
		if _, err := r.db.Query(ctx, "viafunc", q); err == nil {
			t.Errorf("the plugin role ran %s", q)
		}
	}
	r.db.guard = guardSQL
	// Through the SQL guard it cannot name the application role or the admin role.
	r.load(t, nil)
	r.grant(t, sdk.PermDB)
	for _, q := range []string{
		`SET ROLE hm_it_lowapp`, `SET LOCAL ROLE hatamoto_role_admin`, `SELECT hatamoto_admin.provision_plugin_role('other', 'x')`,
	} {
		if o := r.query(t, q); o.Code == "" {
			t.Errorf("%s succeeded: %s", q, o.Result)
		}
	}
	// Uninstall path.
	if err := r.db.Drop(ctx, "viafunc"); err != nil {
		t.Fatal(err)
	}
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM pg_roles WHERE rolname = $1`, r.roleOf(t, "viafunc")).Scan(&n); err != nil || n != 0 {
		t.Fatalf("role survived Drop: %d %v", n, err)
	}
}

// Without CREATEROLE and without the bootstrap the schema (and so kv) still
// works; db.* explains the fix.
func TestWithoutCreateRole(t *testing.T) {
	pool, name := scratchDB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE ROLE hm_plain LOGIN PASSWORD 'x' NOCREATEROLE`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `GRANT CREATE ON DATABASE `+name+` TO hm_plain`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `GRANT ALL ON hatamoto_plugin_kv, hatamoto_plugin_credentials TO hm_plain`); err != nil {
		t.Fatal(err)
	}
	cfg := pool.Config().Copy()
	cfg.ConnConfig.User, cfg.ConnConfig.Password = "hm_plain", "x"
	plain, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()

	db := NewPluginDB(plain, quiet())
	if err := db.Provision(ctx, "lowpriv"); err != nil {
		t.Fatalf("the schema must be created without CREATEROLE: %v", err)
	}
	if err := db.KVSet(ctx, "lowpriv", "k", json.RawMessage(`1`), 0); err != nil {
		t.Fatalf("kv without CREATEROLE: %v", err)
	}
	err = db.RoleError("lowpriv")
	if !errors.Is(err, ErrRoleUnavailable) || !strings.Contains(err.Error(), "role-admin-bootstrap") {
		t.Fatalf("role error = %v", err)
	}
	if _, err := db.Query(ctx, "lowpriv", `SELECT 1`); !errors.Is(err, ErrRoleUnavailable) {
		t.Fatalf("db.query without a role = %v", err)
	}
}

// A plugin reload runs its migrations when db is granted, and reports a failure
// in the plugin's status without unloading it.
func TestGrantRunsMigrations(t *testing.T) {
	r := newPGRig(t)
	good := Assets{Migrations: []Migration{{Version: 1, Name: "1.sql", Stmts: []string{`CREATE TABLE seen (id int)`}}}}
	st := r.rt.LoadWith(context.Background(), capaManifest(nil), plugintest.Build(t, "capa"), good)
	if !st.Loaded || st.DataError != "" {
		t.Fatalf("load without grants: %+v", st)
	}
	var exists bool
	_ = r.pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = 'plugin_capa' AND tablename = 'seen')`).Scan(&exists)
	if exists {
		t.Fatal("migrations ran before db was granted")
	}
	st, err := r.rt.Grant(context.Background(), "capa", []string{sdk.PermDB})
	if err != nil || st.DataError != "" {
		t.Fatalf("grant: %+v %v", st, err)
	}
	_ = r.pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = 'plugin_capa' AND tablename = 'seen')`).Scan(&exists)
	if !exists {
		t.Fatal("granting db did not run the migrations")
	}
	if o := r.query(t, `SELECT count(*) FROM seen`); o.Code != "" {
		t.Fatalf("the plugin cannot use its own table: %+v", o)
	}
}
