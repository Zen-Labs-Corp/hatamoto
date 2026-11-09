//go:build integration

package pluginhost

import (
	"bytes"
	"context"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/core/migrations"
	"hatamoto.dev/sdk"
)

// The plugin install flow, run once with a superuser role admin and once with
// a role that has only CREATEROLE and CREATEDB (the admin role a managed
// PostgreSQL gives out). The database is provisioned in the documented order:
// the admin creates the app role and the database, the app role grants the
// admin CREATE on it, and the admin runs the bootstrap.

type d8Mode int

const (
	d8Superuser d8Mode = iota
	d8CreateRole
)

func (m d8Mode) String() string { return [...]string{"superuser", "createrole-createdb"}[m] }

func TestD8PluginInstall(t *testing.T) {
	for _, mode := range []d8Mode{d8Superuser, d8CreateRole} {
		t.Run(mode.String(), func(t *testing.T) { d8PluginInstall(t, mode) })
	}
}

// d8Exec runs statements on a fresh connection to dsn.
func d8Exec(t *testing.T, dsn string, stmts ...string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func d8PluginInstall(t *testing.T, mode d8Mode) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	psql, err := exec.LookPath("psql")
	if err != nil {
		t.Skip("psql not installed")
	}
	ctx := context.Background()
	super, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { super.Close(ctx) })
	var isSuper bool
	if err := super.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&isSuper); err != nil || !isSuper {
		t.Skip("this test needs a superuser DATABASE_URL")
	}
	sc := super.Config()
	u := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	appRole, adminRole, db := "hm_d8_app_"+u, sc.User, "hm_d8_db_"+u
	adminPW := sc.Password
	userURL := func(role, pw, dbname string) string {
		x := url.URL{Scheme: "postgres", User: url.UserPassword(role, pw), Host: sc.Host + ":" + strconv.Itoa(int(sc.Port)),
			Path: "/" + dbname, RawQuery: "sslmode=disable"}
		return x.String()
	}
	var roles []string
	var dbID string
	t.Cleanup(func() {
		_, _ = super.Exec(ctx, "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)")
		if dbID != "" {
			rows, _ := super.Query(ctx, `SELECT rolname FROM pg_roles WHERE rolname = $1 OR rolname LIKE $2`,
				"hatamoto_role_admin_"+dbID[:8], "hm\\_p\\_"+dbID[:8]+"\\_%")
			var made []string
			for rows != nil && rows.Next() {
				var r string
				_ = rows.Scan(&r)
				made = append(made, r)
			}
			if rows != nil {
				rows.Close()
			}
			for _, r := range made {
				id := pgx.Identifier{r}.Sanitize()
				_, _ = super.Exec(ctx, "REVOKE SET ON PARAMETER temp_file_limit FROM "+id)
				_, _ = super.Exec(ctx, "DROP ROLE IF EXISTS "+id)
			}
		}
		for i := len(roles) - 1; i >= 0; i-- {
			_, _ = super.Exec(ctx, "DROP ROLE IF EXISTS "+roles[i])
		}
	})

	// Provision in the documented order.
	superURL := userURL(sc.User, sc.Password, "postgres")
	if mode == d8Superuser {
		d8Exec(t, superURL, "CREATE ROLE "+appRole+" LOGIN PASSWORD 'x' NOCREATEROLE")
		roles = append(roles, appRole)
		d8Exec(t, superURL, "CREATE DATABASE "+db+" OWNER "+appRole)
	} else {
		adminRole, adminPW = "hm_d8_adm_"+u, "x"
		d8Exec(t, superURL, "CREATE ROLE "+adminRole+" LOGIN PASSWORD 'x' CREATEROLE CREATEDB NOSUPERUSER NOBYPASSRLS")
		roles = append(roles, adminRole)
		adminURL := userURL(adminRole, "x", "postgres")
		d8Exec(t, adminURL, "CREATE ROLE "+appRole+" LOGIN PASSWORD 'x'", "GRANT "+appRole+" TO "+adminRole+" WITH SET TRUE")
		roles = append(roles, appRole)
		d8Exec(t, adminURL, "CREATE DATABASE "+db+" OWNER "+appRole)
		d8Exec(t, userURL(appRole, "x", db), "GRANT CREATE ON DATABASE "+db+" TO "+adminRole)
	}

	cfg, err := pgxpool.ParseConfig(userURL(appRole, "x", db))
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	prov, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), migrations.FS, goose.WithAllowOutofOrder(true))
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
		t.Fatal(err)
	}

	// The admin runs the bootstrap.
	cmd := exec.Command(psql, "-X", "-q", "-v", "ON_ERROR_STOP=1", "-v", "app_role="+appRole, "-d", db,
		"-f", "../../../deploy/sql/role-admin-bootstrap.sql")
	cmd.Env = append(os.Environ(), "PGHOST="+sc.Host, "PGPORT="+strconv.Itoa(int(sc.Port)),
		"PGUSER="+adminRole, "PGPASSWORD="+adminPW)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap as %s: %v\n%s", adminRole, err, out)
	}

	// The app role stays plain.
	var createRole bool
	if err := pool.QueryRow(ctx, `SELECT rolcreaterole FROM pg_roles WHERE rolname = current_user`).Scan(&createRole); err != nil || createRole {
		t.Fatalf("app role rolcreaterole = %v, %v; want false", createRole, err)
	}

	// Install the plugin: its role comes from the role admin, migrations run.
	var logs bytes.Buffer
	r := newCapaRig(t)
	pdb := NewPluginDB(pool, slog.New(slog.NewTextHandler(&logs, nil)))
	grants := NewGrantStore(pool)
	svc := r.rt.caps.api.svc
	svc.DB = pdb
	r.rt.Configure(Config{Services: svc, Grants: grants, Scheduler: r.sched})

	query := func(sql string) outcome {
		return r.do(t, sdk.FnDBQuery, map[string]any{"sql": sql, "args": []any{}})
	}
	assets := Assets{Migrations: []Migration{{Version: 1, Name: "1.sql", Stmts: []string{`CREATE TABLE seen (id int)`}}}}
	st := r.rt.LoadWith(ctx, capaManifest(nil), plugintest.Build(t, "capa"), assets)
	if !st.Loaded {
		t.Fatalf("load: %+v", st)
	}
	if st, err = r.rt.Grant(ctx, "capa", []string{sdk.PermDB}); err != nil || st.DataError != "" {
		t.Fatalf("grant: %+v %v", st, err)
	}
	if err := pdb.RoleError("capa"); err != nil {
		t.Fatalf("role error: %v", err)
	}
	role := roleNameFor(dbID, "capa")
	var admin string
	if err := pool.QueryRow(ctx, `SELECT pg_get_userbyid(proowner) FROM pg_proc WHERE proname = 'provision_plugin_role'`).Scan(&admin); err != nil || admin != "hatamoto_role_admin_"+dbID[:8] {
		t.Fatalf("provision function owner = %q, %v", admin, err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = 'plugin_capa' AND tablename = 'seen' AND tableowner = $1)`, role).Scan(&exists); err != nil || !exists {
		t.Fatalf("migration did not run as the plugin role: %v %v", exists, err)
	}
	if o := query(`SELECT count(*) FROM seen`); o.Code != "" {
		t.Fatalf("the plugin cannot use its own table: %+v", o)
	}

	// Isolation: through the host guard, and with the guard off.
	for _, q := range []string{`SELECT * FROM public.hatamoto_users`, `SELECT * FROM hatamoto_plugin_kv`, `SET ROLE ` + appRole} {
		if o := query(q); o.Code == "" || strings.Contains(string(o.Result), "secret@example.com") {
			t.Errorf("%q was not refused: %+v", q, o)
		}
	}
	pdb.guard = unguarded
	for _, q := range []string{`SELECT * FROM public.hatamoto_users`, `SELECT * FROM hatamoto_plugin_credentials`} {
		if _, err := pdb.Query(ctx, "capa", q); err == nil {
			t.Errorf("the plugin role ran %q", q)
		}
	}
	pdb.guard = guardSQL

	// No core object is open to PUBLIC.
	if err := pdb.guardPublicObjects(ctx); err != nil {
		t.Fatalf("exposure check: %v", err)
	}

	// Only a role admin that may not grant the parameter leaves it unset.
	wantOff := mode == d8CreateRole
	if got := pdb.TempFileLimitOff("capa"); got != wantOff {
		t.Fatalf("TempFileLimitOff = %v, want %v", got, wantOff)
	}
	warns := strings.Count(logs.String(), "level=WARN")
	if wantOff && (warns != 1 || strings.Count(logs.String(), "temp_file_limit not applied") != 1) {
		t.Fatalf("want exactly one temp_file_limit warning:\n%s", logs.String())
	}
	if !wantOff && warns != 0 {
		t.Fatalf("unexpected warnings:\n%s", logs.String())
	}
}
