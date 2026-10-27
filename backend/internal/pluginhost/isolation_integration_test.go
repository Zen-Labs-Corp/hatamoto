//go:build integration

package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/migrations"
)

// These tests prove the database, not the SQL guard, confines plugin SQL: the
// plugin runs on a pool whose session user is its own LOGIN role. Every test
// that attacks switches the guard off first (unguarded), so a bypass of the
// guard is exactly the situation under test.

func unguarded(sql string, _ sqlMode) (string, error) { return sql, nil }

func eachMode(t *testing.T, fn func(t *testing.T, mode roleMode)) {
	for _, mode := range []roleMode{modeCreateRole, modeBootstrap} {
		t.Run(mode.String(), func(t *testing.T) { fn(t, mode) })
	}
}

func appRoleOf(t *testing.T, r *pgRig) string {
	t.Helper()
	var u string
	if err := r.pool.QueryRow(context.Background(), `SELECT current_user`).Scan(&u); err != nil {
		t.Fatal(err)
	}
	return u
}

// coreIntact fails the test when anything plugin SQL did could be seen by core.
func coreIntact(t *testing.T, r *pgRig, app string) {
	t.Helper()
	ctx := context.Background()
	var cu, sp string
	var n int
	var hash string
	if err := r.pool.QueryRow(ctx, `SELECT current_user, current_setting('search_path')`).Scan(&cu, &sp); err != nil {
		t.Fatal(err)
	}
	if cu != app || strings.Contains(sp, "plugin_") {
		t.Fatalf("core connection state: user=%s search_path=%s", cu, sp)
	}
	if err := r.pool.QueryRow(ctx, `SELECT count(*), min(password_hash) FROM hatamoto_users`).Scan(&n, &hash); err != nil || n != 1 || hash != "x" {
		t.Fatalf("core users: n=%d hash=%q err=%v", n, hash, err)
	}
	var viaPublic bool
	if err := r.pool.QueryRow(ctx, `SELECT 'hatamoto_users'::regclass = 'public.hatamoto_users'::regclass`).Scan(&viaPublic); err != nil || !viaPublic {
		t.Fatalf("hatamoto_users no longer resolves to public: %v %v", viaPublic, err)
	}
}

// The escape payloads. Each must fail, or at least return nothing of core's.
func escapePayloads(app string) map[string]string {
	return map[string]string{
		"query_to_xml set_config role":   `SELECT query_to_xml('select set_config(''role'',''none'',true); select current_user as u, email, password_hash from public.hatamoto_users', false, false, '')`,
		"query_to_xml role then reset":   `SELECT query_to_xml('reset role; select email from public.hatamoto_users', false, false, '')`,
		"query_to_xml set_config path":   `SELECT query_to_xml('select set_config(''search_path'',''public'',true); select email from hatamoto_users', false, false, '')`,
		"query_to_xml session auth":      `SELECT query_to_xml('set session authorization default; select email from hatamoto_users', false, false, '')`,
		"query_to_xml set role app":      `SELECT query_to_xml('set role ` + app + `; select email from hatamoto_users', false, false, '')`,
		"set_config role to app":         `SELECT set_config('role', '` + app + `', false)`,
		"ts_stat":                        `SELECT * FROM ts_stat('select to_tsvector(email) from hatamoto_users')`,
		"set session authorization":      `SET SESSION AUTHORIZATION ` + app,
		"set session authorization dflt": `SET SESSION AUTHORIZATION DEFAULT`,
		"set role app":                   `SET ROLE ` + app,
		"core table":                     `SELECT * FROM public.hatamoto_users`,
		"core table unqualified":         `SELECT * FROM hatamoto_users`,
		"core write":                     `UPDATE public.hatamoto_users SET password_hash = 'owned'`,
		"core delete":                    `DELETE FROM public.hatamoto_users`,
		"credentials":                    `SELECT * FROM public.hatamoto_plugin_credentials`,
		"pg_authid":                      `SELECT * FROM pg_catalog.pg_authid`,
		"pg_shadow":                      `SELECT * FROM pg_catalog.pg_shadow`,
		"copy to program":                `COPY (SELECT 1) TO PROGRAM 'id'`,
		"copy table":                     `COPY public.hatamoto_users TO STDOUT`,
		"pg_read_file":                   `SELECT pg_read_file('/etc/passwd')`,
		"pg_ls_dir":                      `SELECT pg_ls_dir('/')`,
		"lo_import":                      `SELECT lo_import('/etc/passwd')`,
		"dblink":                         `SELECT * FROM dblink('dbname=postgres', 'select 1') AS t(a int)`,
		"create extension dblink":        `CREATE EXTENSION dblink`,
		"create function":                `CREATE FUNCTION public.f() RETURNS int LANGUAGE sql AS 'select 1'`,
		"create in public":               `CREATE TABLE public.evil (id int)`,
		"create role":                    `CREATE ROLE evil LOGIN`,
		"alter role self":                `ALTER ROLE CURRENT_USER SUPERUSER`,
		"do block":                       `DO $$ BEGIN PERFORM set_config('role', '` + app + `', false); END $$`,
	}
}

func TestPluginEscapesHoldWithoutTheGuard(t *testing.T) {
	eachMode(t, func(t *testing.T, mode roleMode) {
		r := newPGRigMode(t, mode)
		r.db.guard = unguarded
		ctx := context.Background()
		app := appRoleOf(t, r)
		if err := r.db.Provision(ctx, "capa"); err != nil {
			t.Fatal(err)
		}
		if err := r.db.RoleError("capa"); err != nil {
			t.Fatal(err)
		}
		role := r.roleOf(t, "capa")
		// The plugin's sessions run as the plugin role, whatever the SQL does.
		res, err := r.db.Query(ctx, "capa", `SELECT session_user::text, current_user::text`)
		if err != nil || res.Rows[0][0] != role || res.Rows[0][1] != role {
			t.Fatalf("session user: %+v %v", res, err)
		}

		for name, sql := range escapePayloads(app) {
			res, err := r.db.Query(ctx, "capa", sql)
			if err == nil {
				b, _ := json.Marshal(res)
				if strings.Contains(string(b), "secret@example.com") || strings.Contains(string(b), app) {
					t.Errorf("%s leaked: %s", name, b)
				}
				// SET/RESET-style statements may succeed as no-ops; the state is checked below.
				switch name {
				case "set session authorization dflt":
				default:
					t.Errorf("%s: expected the database to refuse %q, got %s", name, sql, b)
				}
			}
			// After any attempt the next plugin session is still the plugin role.
			res, err = r.db.Query(ctx, "capa", `SELECT session_user::text, current_user::text, current_setting('search_path')`)
			if err != nil || res.Rows[0][0] != role || res.Rows[0][1] != role || res.Rows[0][2] != "plugin_capa" {
				t.Fatalf("after %s: %+v %v", name, res, err)
			}
		}
		coreIntact(t, r, app)
	})
}

// The one place the same payloads run through the guard as well.
func TestEscapePayloadsThroughTheGuard(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	app := appRoleOf(t, r)
	if err := r.db.Provision(ctx, "capa"); err != nil {
		t.Fatal(err)
	}
	for name, sql := range escapePayloads(app) {
		if res, err := r.db.Query(ctx, "capa", sql); err == nil {
			b, _ := json.Marshal(res)
			t.Errorf("%s was not refused: %s", name, b)
		}
	}
	coreIntact(t, r, app)
}

func TestPluginMigrationCannotTouchCore(t *testing.T) {
	eachMode(t, func(t *testing.T, mode roleMode) {
		r := newPGRigMode(t, mode)
		ctx := context.Background()
		app := appRoleOf(t, r)
		if err := r.db.Provision(ctx, "capa"); err != nil {
			t.Fatal(err)
		}
		stmts := []string{
			`SELECT query_to_xml('select set_config(''role'', ''none'', true), set_config(''search_path'', ''public'', true)', false, false, '')`,
			`UPDATE hatamoto_users SET password_hash = 'owned'`,
		}
		// The guard refuses it up front ...
		for _, s := range stmts {
			if _, err := guardSQL(s, sqlMigration); err == nil {
				if s == stmts[1] {
					continue // an UPDATE on its own is plain DML; the first statement is the escape
				}
				t.Fatalf("the guard accepted %q", s)
			}
		}
		// ... and with the guard off the database refuses it.
		r.db.guard = unguarded
		n, err := r.db.Migrate(ctx, "capa", []Migration{{Version: 1, Name: "1_evil.sql", Stmts: stmts}})
		if err == nil {
			t.Fatalf("the migration ran (%d applied)", n)
		}
		coreIntact(t, r, app)
		// A fresh table named like a core table stays in the plugin's schema.
		if _, err := r.db.Migrate(ctx, "capa", []Migration{{Version: 2, Name: "2_shadow.sql", Stmts: []string{`CREATE TABLE hatamoto_users (id int)`}}}); err != nil {
			t.Fatalf("a plugin table named hatamoto_users: %v", err)
		}
		coreIntact(t, r, app)
	})
}

func TestPluginSessionStateDoesNotLeak(t *testing.T) {
	eachMode(t, func(t *testing.T, mode roleMode) {
		r := newPGRigMode(t, mode)
		r.db.guard = unguarded
		ctx := context.Background()
		app := appRoleOf(t, r)
		if err := r.db.Provision(ctx, "capa"); err != nil {
			t.Fatal(err)
		}
		for name, sql := range map[string]string{
			"role":        `SELECT query_to_xml('select set_config(''role'',''none'',false)', false, false, '')`,
			"search_path": `SELECT set_config('search_path', 'plugin_capa, public', false)`,
			"timeout":     `SELECT set_config('statement_timeout', '0', false)`,
			"temp table":  `SELECT 1 AS id INTO TEMP TABLE hatamoto_users`,
			"temp named":  `CREATE TEMP TABLE hatamoto_sessions (id int)`,
			"adv lock":    `SELECT pg_advisory_lock(hashtext('hatamoto.plugin.provision'))`,
			"adv lock 2":  `SELECT query_to_xml('select pg_advisory_lock(hashtext(''hatamoto.plugin.provision''))', false, false, '')`,
		} {
			if _, err := r.db.Query(ctx, "capa", sql); err != nil {
				t.Logf("%s: %v", name, err)
			}
			// Core never shares a connection with the plugin: nothing shows.
			coreIntact(t, r, app)
		}
		// The plugin pool hands the next call a clean session (DISCARD ALL on release).
		deadline := time.Now().Add(3 * time.Second)
		for {
			res, err := r.db.Query(ctx, "capa", `SELECT
				(SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND pid = pg_backend_pid()),
				(SELECT count(*) FROM pg_class WHERE relpersistence = 't' AND relkind = 'r'),
				current_setting('statement_timeout')`)
			if err != nil {
				t.Fatal(err)
			}
			locks, temps := res.Rows[0][0], res.Rows[0][1]
			if locks == int64(0) && temps == int64(0) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("plugin session state kept across calls: %+v", res)
			}
			time.Sleep(50 * time.Millisecond)
		}
		// The provisioning lock is free for core (a plugin held it only inside its own session).
		// The pool holds two connections and the idle one may not have run
		// DISCARD ALL yet, so poll within the same deadline. The xact-scoped
		// try-lock releases itself when the statement ends.
		for {
			var ok bool
			if err := r.pool.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtext('hatamoto.plugin.provision'))`).Scan(&ok); err != nil {
				t.Fatal(err)
			}
			if ok {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the provision lock stayed held")
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
}

func TestPluginSeesNoOtherSessionsQueries(t *testing.T) {
	r := newPGRig(t)
	r.db.guard = unguarded
	ctx := context.Background()
	if err := r.db.Provision(ctx, "capa"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = r.pool.Exec(ctx, `SELECT pg_sleep(3) /* hm-secret-marker */`)
	}()
	time.Sleep(400 * time.Millisecond)
	res, err := r.db.Query(ctx, "capa", `SELECT usename::text, state, query FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid()`)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, row := range res.Rows {
		seen++
		if q, _ := row[2].(string); strings.Contains(q, "hm-secret-marker") || strings.Contains(q, "hatamoto_") {
			t.Errorf("pg_stat_activity shows another role's query: %v", row)
		}
	}
	t.Logf("%d other sessions listed, none with query text", seen)
	// It may not cancel them either.
	res, err = r.db.Query(ctx, "capa", `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid() AND usename = '`+appRoleOf(t, r)+`'`)
	if err == nil {
		for _, row := range res.Rows {
			if row[0] == true {
				t.Fatal("the plugin terminated a core backend")
			}
		}
	}
	<-done
}

// superExec runs sql as the superuser behind DATABASE_URL in r's database
// (skipping the test when DATABASE_URL is not a superuser).
func (r *pgRig) superExec(t *testing.T, sql string) {
	t.Helper()
	ctx := context.Background()
	var name string
	if err := r.pool.QueryRow(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = name
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	var super bool
	if err := c.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super); err != nil || !super {
		t.Skip("needs a superuser DATABASE_URL")
	}
	if _, err := c.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
}

func TestPluginRoleHasNoCorePrivileges(t *testing.T) {
	eachMode(t, func(t *testing.T, mode roleMode) {
		r := newPGRigMode(t, mode)
		r.db.guard = unguarded
		ctx := context.Background()

		// Objects the application role does not own stay open to PUBLIC however
		// the migration ran: a postgres-owned table and a SECURITY DEFINER
		// function. The host refuses db.* while they exist.
		r.superExec(t, `CREATE TABLE public.legacy_secrets (v text);
			INSERT INTO public.legacy_secrets VALUES ('s3cret');
			GRANT SELECT ON public.legacy_secrets TO PUBLIC;
			CREATE FUNCTION public.su_fn() RETURNS int LANGUAGE sql SECURITY DEFINER AS 'select 1';`)
		if err := r.db.Provision(ctx, "capa"); err != nil {
			t.Fatal(err)
		}
		err := r.db.RoleError("capa")
		if !errors.Is(err, ErrRoleUnavailable) || !strings.Contains(err.Error(), "public.legacy_secrets") || !strings.Contains(err.Error(), "public.su_fn") {
			t.Fatalf("open PUBLIC objects must refuse db.*, naming them: %v", err)
		}
		if _, err := r.db.Query(ctx, "capa", `SELECT 1`); !errors.Is(err, ErrRoleUnavailable) {
			t.Fatalf("db.query with open PUBLIC objects: %v", err)
		}
		if objs, err := r.db.PublicObjects(ctx); err != nil || len(objs) != 2 {
			t.Fatalf("PublicObjects = %v %v", objs, err)
		}
		// The operator may accept the risk; then the exposure is real.
		r.db.AllowPublicObjects = true
		if err := r.db.Reprovision(ctx, "capa"); err != nil || r.db.RoleError("capa") != nil {
			t.Fatalf("allowed: %v / %v", err, r.db.RoleError("capa"))
		}
		if res, err := r.db.Query(ctx, "capa", `SELECT v FROM public.legacy_secrets`); err != nil || len(res.Rows) != 1 {
			t.Fatalf("expected the allowed exposure to be readable: %v %v", res, err)
		}
		// Once the owner closes them, db.* is back without the override.
		r.db.AllowPublicObjects = false
		r.superExec(t, `REVOKE ALL ON public.legacy_secrets FROM PUBLIC; REVOKE ALL ON FUNCTION public.su_fn() FROM PUBLIC;`)
		if err := r.db.Reprovision(ctx, "capa"); err != nil || r.db.RoleError("capa") != nil {
			t.Fatalf("after the owner closed them: %v / %v", err, r.db.RoleError("capa"))
		}
		if _, err := r.db.Query(ctx, "capa", `SELECT v FROM public.legacy_secrets`); err == nil {
			t.Fatal("a closed table is still readable")
		}
		r.superExec(t, `DROP TABLE public.legacy_secrets; DROP FUNCTION public.su_fn()`)
		// Seen by the plugin role itself: nothing in public is usable.
		res, err := r.db.Query(ctx, "capa", `SELECT
			(SELECT count(*) FROM pg_class c WHERE c.relnamespace = 'public'::regnamespace AND c.relkind IN ('r','p','v','m','S')
				AND (has_table_privilege(c.oid, 'SELECT') OR has_table_privilege(c.oid, 'INSERT') OR has_table_privilege(c.oid, 'UPDATE')
				     OR has_table_privilege(c.oid, 'DELETE') OR has_table_privilege(c.oid, 'REFERENCES') OR has_table_privilege(c.oid, 'TRIGGER'))),
			(SELECT count(*) FROM pg_proc p WHERE p.pronamespace = 'public'::regnamespace AND has_function_privilege(p.oid, 'EXECUTE')
				AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e')),
			has_schema_privilege('public', 'CREATE'),
			has_database_privilege(current_database(), 'CREATE'),
			(SELECT count(*) FROM pg_auth_members WHERE member = (SELECT oid FROM pg_roles WHERE rolname = current_user)),
			(SELECT rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication OR rolbypassrls FROM pg_roles WHERE rolname = current_user)`)
		if err != nil {
			t.Fatal(err)
		}
		row := res.Rows[0]
		if row[0] != int64(0) || row[1] != int64(0) || row[2] != false || row[3] != false || row[4] != int64(0) || row[5] != false {
			t.Fatalf("plugin role privileges (tables, functions, public CREATE, db CREATE, memberships, attrs): %v", row)
		}
		role := r.roleOf(t, "capa")
		// Seen by core: the role is exactly what the docs promise.
		var login, super, cdb, crole, repl, bypass bool
		var limit int
		var cfg []string
		var owner string
		if err := r.pool.QueryRow(ctx, `SELECT rolcanlogin, rolsuper, rolcreatedb, rolcreaterole, rolreplication, rolbypassrls, rolconnlimit,
			coalesce(rolconfig, '{}'), (SELECT nspowner::regrole::text FROM pg_namespace WHERE nspname = 'plugin_capa')
			FROM pg_roles WHERE rolname = $1`, role).Scan(&login, &super, &cdb, &crole, &repl, &bypass, &limit, &cfg, &owner); err != nil {
			t.Fatal(err)
		}
		if !login || super || cdb || crole || repl || bypass || limit != pluginConnLimit || owner != role {
			t.Fatalf("role attributes: login=%v super=%v cdb=%v crole=%v repl=%v bypass=%v limit=%d owner=%s", login, super, cdb, crole, repl, bypass, limit, owner)
		}
		joined := strings.Join(cfg, ",")
		for _, want := range []string{"search_path=plugin_capa", "statement_timeout=10s", "lock_timeout=2s", "idle_in_transaction_session_timeout=15s"} {
			if !strings.Contains(joined, want) {
				t.Errorf("role setting %s missing from %v", want, cfg)
			}
		}
		var n int
		if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM pg_auth_members WHERE member = $1::regrole`, role).Scan(&n); err != nil || n != 0 {
			t.Fatalf("the plugin role is a member of %d roles (%v)", n, err)
		}
		// Core tables grant nothing to PUBLIC.
		if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM pg_class c WHERE c.relnamespace = 'public'::regnamespace
			AND c.relkind IN ('r','p','v','m','S') AND has_table_privilege('public', c.oid, 'SELECT,INSERT,UPDATE,DELETE')`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%d public tables are open to PUBLIC (%v)", n, err)
		}
	})
}

func TestPluginPoolFailureIsUnavailable(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	if err := r.db.Provision(ctx, "capa"); err != nil {
		t.Fatal(err)
	}
	r.db.mu.Lock()
	r.db.state["capa"].password = "not the password"
	r.db.mu.Unlock()
	_, err := r.db.Query(ctx, "capa", `SELECT 1`)
	if !errors.Is(err, ErrRoleUnavailable) || !strings.Contains(err.Error(), "pg_hba.conf") {
		t.Fatalf("a pool that cannot log in: %v", err)
	}
	// kv is core's and keeps working.
	if err := r.db.KVSet(ctx, "capa", "k", json.RawMessage(`1`), 0); err != nil {
		t.Fatal(err)
	}
}

func TestPluginPasswordRotation(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	if err := r.db.Provision(ctx, "capa"); err != nil {
		t.Fatal(err)
	}
	stored := func() string {
		var pw string
		if err := r.pool.QueryRow(ctx, `SELECT password FROM hatamoto_plugin_credentials WHERE plugin = 'capa'`).Scan(&pw); err != nil {
			t.Fatal(err)
		}
		return pw
	}
	pw1 := stored()
	if len(pw1) < 40 {
		t.Fatalf("password too short: %d", len(pw1))
	}
	if _, err := r.db.Query(ctx, "capa", `SELECT 1`); err != nil {
		t.Fatal(err)
	}
	// A fresh process (a new PluginDB) keeps the stored password.
	db2 := NewPluginDB(r.pool, quiet())
	if err := db2.Provision(ctx, "capa"); err != nil || stored() != pw1 {
		t.Fatalf("a restart rotated the password: %v", err)
	}
	if _, err := db2.Query(ctx, "capa", `SELECT 1`); err != nil {
		t.Fatalf("after restart: %v", err)
	}
	db2.Close()

	if err := r.db.Reprovision(ctx, "capa"); err != nil {
		t.Fatal(err)
	}
	pw2 := stored()
	if pw2 == pw1 {
		t.Fatal("Reprovision kept the password")
	}
	if _, err := r.db.Query(ctx, "capa", `SELECT 1`); err != nil {
		t.Fatalf("after rotation: %v", err)
	}
	// The old password no longer logs in.
	cc := r.pool.Config().ConnConfig.Copy()
	cc.User, cc.Password = r.roleOf(t, "capa"), pw1
	if c, err := pgx.ConnectConfig(ctx, cc); err == nil {
		c.Close(ctx)
		t.Fatal("the old password still works")
	}
	cc.Password = pw2
	c, err := pgx.ConnectConfig(ctx, cc)
	if err != nil {
		t.Fatalf("the new password: %v", err)
	}
	c.Close(ctx)
}

func TestKVQuota(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	if err := r.db.Provision(ctx, "capa"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO hatamoto_plugin_kv (plugin, key, value) SELECT 'capa', 'k' || g, '1' FROM generate_series(1, $1) g`, maxKVKeys); err != nil {
		t.Fatal(err)
	}
	if err := r.db.KVSet(ctx, "capa", "one-too-many", json.RawMessage(`1`), 0); !errors.Is(err, ErrKVQuota) {
		t.Fatalf("a full store accepted a new key: %v", err)
	}
	if err := r.db.KVSet(ctx, "capa", "k1", json.RawMessage(`2`), 0); err != nil {
		t.Fatalf("overwriting a key in a full store: %v", err)
	}
	if err := r.db.KVSet(ctx, "other", "k", json.RawMessage(`2`), 0); err != nil {
		t.Fatalf("the quota is per plugin: %v", err)
	}
}

// Two Hatamoto databases in one cluster: roles are cluster-wide, so the second
// database provisioning the same plugin name must neither disturb the first
// database's role nor gain any right on it.
func TestTwoDatabasesOneCluster(t *testing.T) {
	eachMode(t, func(t *testing.T, mode roleMode) {
		ctx := context.Background()
		poolA, _ := scratchDBMode(t, mode)
		poolB, _ := scratchDBRole(t, mode, map[roleMode]string{modeCreateRole: "hm_it_app2", modeBootstrap: "hm_it_lowapp2"}[mode])
		dbA, dbB := NewPluginDB(poolA, quiet()), NewPluginDB(poolB, quiet())
		stored := func(p *pgxpool.Pool) string {
			var pw string
			if err := p.QueryRow(ctx, `SELECT password FROM hatamoto_plugin_credentials WHERE plugin = 'foo'`).Scan(&pw); err != nil {
				t.Fatal(err)
			}
			return pw
		}
		idA, _ := instanceID(ctx, poolA)
		idB, _ := instanceID(ctx, poolB)
		roleA, roleB := roleNameFor(idA, "foo"), roleNameFor(idB, "foo")
		if roleA == roleB {
			t.Fatalf("both databases name their role %s", roleA)
		}

		if err := dbA.Provision(ctx, "foo"); err != nil || dbA.RoleError("foo") != nil {
			t.Fatalf("A: %v / %v", err, dbA.RoleError("foo"))
		}
		if _, err := dbA.Migrate(ctx, "foo", []Migration{{Version: 1, Name: "1.sql", Stmts: []string{`CREATE TABLE notes (v text)`, `INSERT INTO notes VALUES ('a')`}}}); err != nil {
			t.Fatal(err)
		}
		pwA := stored(poolA)

		// B provisions, rotates and drops "foo" too.
		if err := dbB.Provision(ctx, "foo"); err != nil || dbB.RoleError("foo") != nil {
			t.Fatalf("B: %v / %v", err, dbB.RoleError("foo"))
		}
		if err := dbB.Reprovision(ctx, "foo"); err != nil || dbB.RoleError("foo") != nil {
			t.Fatalf("B reprovision: %v / %v", err, dbB.RoleError("foo"))
		}
		if _, err := dbB.Exec(ctx, "foo", `SELECT 1`); err != nil {
			t.Fatalf("B's own role: %v", err)
		}
		if got := stored(poolA); got != pwA {
			t.Fatal("B changed A's stored password")
		}
		// A's password still logs in: a fresh PluginDB over A (no cached pool) connects.
		fresh := NewPluginDB(poolA, quiet())
		if err := fresh.Provision(ctx, "foo"); err != nil {
			t.Fatal(err)
		}
		if res, err := fresh.Query(ctx, "foo", `SELECT v FROM notes`); err != nil || len(res.Rows) != 1 {
			t.Fatalf("A's role after B provisioned: %v %v", res, err)
		}
		fresh.Close()
		if res, err := dbA.Query(ctx, "foo", `SELECT v FROM notes`); err != nil || len(res.Rows) != 1 {
			t.Fatalf("A's open pool after B provisioned: %v %v", res, err)
		}
		// B's application role has no SET (or any membership) on A's plugin role.
		var appB string
		if err := poolB.QueryRow(ctx, `SELECT current_user`).Scan(&appB); err != nil {
			t.Fatal(err)
		}
		var set, member bool
		if err := poolB.QueryRow(ctx, `SELECT pg_has_role($1, $2, 'SET'), pg_has_role($1, $2, 'MEMBER')`, appB, roleA).Scan(&set, &member); err != nil || set || member {
			t.Fatalf("B's app role holds A's plugin role: set=%v member=%v %v", set, member, err)
		}
		// B dropping its foo leaves A's alone.
		if err := dbB.Drop(ctx, "foo"); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := poolA.QueryRow(ctx, `SELECT count(*) FROM pg_roles WHERE rolname = $1`, roleA).Scan(&n); err != nil || n != 1 {
			t.Fatalf("A's role after B's drop: %d %v", n, err)
		}
		if _, err := dbA.Query(ctx, "foo", `SELECT v FROM notes`); err != nil {
			t.Fatalf("A after B's drop: %v", err)
		}

		// A role of the right name that this database did not create is refused,
		// never altered or granted.
		rB := &pgRig{pool: poolB}
		foreign := roleNameFor(idB, "zzz")
		rB.superExec(t, `CREATE ROLE `+pgx.Identifier{foreign}.Sanitize()+` NOLOGIN`)
		if err := dbB.Provision(ctx, "zzz"); err != nil {
			t.Fatal(err)
		}
		if err := dbB.RoleError("zzz"); !errors.Is(err, ErrRoleUnavailable) {
			t.Fatalf("a role not created for this database must be refused: %v", err)
		}
		var login bool
		if err := poolB.QueryRow(ctx, `SELECT rolcanlogin OR pg_has_role($2, $1, 'SET') FROM pg_roles WHERE rolname = $1`, foreign, appB).Scan(&login); err != nil || login {
			t.Fatalf("the foreign role was altered or granted: %v %v", login, err)
		}
	})
}

// The PUBLIC revoke must cope with every routine kind in public.
func TestMigrationRevokesProceduresAndAggregates(t *testing.T) {
	pool, _ := scratchDB(t)
	ctx := context.Background()
	prov, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prov.DownTo(ctx, 202610160900); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, q := range []string{
		`CREATE PROCEDURE public.p_cleanup() LANGUAGE sql AS 'select 1'`,
		`CREATE FUNCTION public.f_add(a int, b int) RETURNS int LANGUAGE sql AS 'select a + b'`,
		`CREATE AGGREGATE public.a_sum2(int) (sfunc = int4pl, stype = int, initcond = '0')`,
		`GRANT EXECUTE ON ROUTINE public.p_cleanup(), public.f_add(int, int), public.a_sum2(int) TO PUBLIC`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := prov.Up(ctx); err != nil {
		t.Fatalf("migrate with a procedure and an aggregate in public: %v", err)
	}
	var open int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_proc p WHERE p.pronamespace = 'public'::regnamespace
		AND p.proname IN ('p_cleanup', 'f_add', 'a_sum2') AND has_function_privilege('public', p.oid, 'EXECUTE')`).Scan(&open); err != nil || open != 0 {
		t.Fatalf("%d routines still open to PUBLIC (%v)", open, err)
	}
}
