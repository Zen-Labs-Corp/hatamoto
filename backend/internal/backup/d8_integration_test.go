//go:build integration

package backup

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/migrations"
)

// These tests run the whole backup and restore flow twice, once with a
// superuser admin and once with a role that has only CREATEROLE and CREATEDB
// (the shape of the admin role a managed PostgreSQL gives out). The databases
// are provisioned in the documented order for that role: it joins the app
// role it creates, then creates the database for it. They need what the restore integration tests need and skip
// otherwise.

type adminMode int

const (
	adminSuperuser adminMode = iota
	adminCreateRole
)

func (m adminMode) String() string { return [...]string{"superuser", "createrole-createdb"}[m] }

// d8Cluster is one test's view of the cluster: unique role and database names,
// everything it made removed again when the test ends.
type d8Cluster struct {
	t         *testing.T
	super     *pgx.Conn
	mode      adminMode
	host      string
	adminRole string // the role that runs the bootstrap and the restore's admin steps
	appRole   string
	id8       string // the site's database id prefix, once known
	dbs       []string
	roles     []string
}

func newD8Cluster(t *testing.T, mode adminMode) *d8Cluster {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	super, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { super.Close(ctx) })
	var isSuper bool
	if err := super.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&isSuper); err != nil {
		t.Fatal(err)
	}
	if !isSuper {
		t.Skip("these tests need a superuser DATABASE_URL")
	}
	if _, err := exec.LookPath("psql"); err != nil {
		t.Skip("psql not installed")
	}
	cfg := super.Config()
	u := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	c := &d8Cluster{t: t, super: super, mode: mode, host: cfg.Host + ":" + strconv.Itoa(int(cfg.Port)),
		appRole: "hm_d8_app_" + u, adminRole: cfg.User}
	t.Cleanup(c.cleanup)
	if mode == adminCreateRole {
		c.adminRole = "hm_d8_adm_" + u
		c.makeRole(c.adminRole, "CREATEROLE CREATEDB NOSUPERUSER NOBYPASSRLS")
	}
	return c
}

func (c *d8Cluster) makeRole(name, attrs string) {
	c.t.Helper()
	if _, err := c.super.Exec(context.Background(), "CREATE ROLE "+name+" LOGIN PASSWORD 'x' "+attrs); err != nil {
		c.t.Fatal(err)
	}
	c.roles = append(c.roles, name)
}

// url is a DSN for a role the test created (or the superuser) on a database.
func (c *d8Cluster) url(role, db string) string {
	user := url.UserPassword(role, "x")
	if role == c.super.Config().User {
		user = url.UserPassword(role, c.super.Config().Password)
	}
	u := url.URL{Scheme: "postgres", User: user, Host: c.host, Path: "/" + db, RawQuery: "sslmode=disable"}
	return u.String()
}

func (c *d8Cluster) adminURL(db string) string { return c.url(c.adminRole, db) }
func (c *d8Cluster) appURL(db string) string   { return c.url(c.appRole, db) }

func (c *d8Cluster) exec(raw string, stmts ...string) {
	c.t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, raw)
	if err != nil {
		c.t.Fatal(err)
	}
	defer conn.Close(ctx)
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			c.t.Fatalf("%s: %v", s, err)
		}
	}
}

// provisionApp makes the app role (once) and a database it owns, readable and
// creatable for the admin role, the way the managed-PostgreSQL guide says.
func (c *d8Cluster) provisionApp(db string) {
	c.t.Helper()
	c.dbs = append(c.dbs, db)
	if c.mode == adminSuperuser {
		if len(c.roles) == 0 {
			c.makeRole(c.appRole, "NOCREATEROLE")
		}
		c.exec(c.url(c.super.Config().User, "postgres"), "CREATE DATABASE "+db+" OWNER "+c.appRole)
		return
	}
	admin := c.adminURL("postgres")
	if !c.has(c.appRole) {
		c.exec(admin, "CREATE ROLE "+c.appRole+" LOGIN PASSWORD 'x'", "GRANT "+c.appRole+" TO "+c.adminRole+" WITH SET TRUE")
		c.roles = append(c.roles, c.appRole)
	}
	c.exec(admin, "CREATE DATABASE "+db+" OWNER "+c.appRole)
	// No grant on the database: the admin inherits CREATE, with its grant option,
	// from the membership in the app role, as the guide says.
}

func (c *d8Cluster) has(role string) bool {
	for _, r := range c.roles {
		if r == role {
			return true
		}
	}
	return false
}

// bootstrap runs role-admin-bootstrap.sql as the admin role and returns psql's output.
func (c *d8Cluster) bootstrap(db string) string {
	c.t.Helper()
	out, err := c.bootstrapAs(db, c.adminRole)
	if err != nil {
		c.t.Fatalf("bootstrap as %s: %v\n%s", c.adminRole, err, out)
	}
	return out
}

// bootstrapAs runs the script as a role the test made (or the admin role).
func (c *d8Cluster) bootstrapAs(db, role string) (string, error) {
	c.t.Helper()
	cfg := c.super.Config()
	pw := "x"
	if role == cfg.User {
		pw = cfg.Password
	}
	cmd := exec.Command("psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "-v", "app_role="+c.appRole, "-d", db,
		"-f", "../../../deploy/sql/role-admin-bootstrap.sql")
	cmd.Env = append(os.Environ(), "PGHOST="+cfg.Host, "PGPORT="+strconv.Itoa(int(cfg.Port)),
		"PGUSER="+role, "PGPASSWORD="+pw)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// cleanup drops everything by the superuser, after the databases.
func (c *d8Cluster) cleanup() {
	ctx := context.Background()
	for _, db := range c.dbs {
		_, _ = c.super.Exec(ctx, "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)")
	}
	if c.id8 != "" { // the roles the site's bootstrap and plugins made
		rows, _ := c.super.Query(ctx, `SELECT rolname FROM pg_roles WHERE rolname = $1 OR rolname LIKE $2`,
			"hatamoto_role_admin_"+c.id8, "hm\\_p\\_"+c.id8+"\\_%")
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
			_, _ = c.super.Exec(ctx, "REVOKE SET ON PARAMETER temp_file_limit FROM "+id)
			_, _ = c.super.Exec(ctx, "DROP ROLE IF EXISTS "+id)
		}
	}
	for i := len(c.roles) - 1; i >= 0; i-- {
		_, _ = c.super.Exec(ctx, "DROP ROLE IF EXISTS "+c.roles[i])
	}
}

// migrate runs the core migrations as the app role and returns its pool.
func (c *d8Cluster) migrate(db string) *pgxpool.Pool {
	c.t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, c.appURL(db))
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(pool.Close)
	prov, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := prov.Up(ctx); err != nil {
		c.t.Fatalf("core migrations as the app role: %v", err)
	}
	return pool
}

func TestBackupRestoreAsSuperuserAndCreateRoleAdmin(t *testing.T) {
	for _, mode := range []adminMode{adminSuperuser, adminCreateRole} {
		t.Run(mode.String(), func(t *testing.T) { backupRestoreFlow(t, mode) })
	}
}

func backupRestoreFlow(t *testing.T, mode adminMode) {
	ctx := context.Background()
	c := newD8Cluster(t, mode)
	name := "hm_d8_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	c.provisionApp(name)

	// A function planted in public by the app role must not be picked up by the
	// bootstrap (it pins search_path), or it would run as the role admin.
	c.exec(c.appURL(name), `CREATE FUNCTION public.format(text, text) RETURNS text LANGUAGE sql AS 'SELECT 1/0'`)
	// The bootstrap: the NOTICE appears once for the limited role, never for a
	// superuser, and a rerun changes nothing.
	for run := 1; run <= 2; run++ {
		out := c.bootstrap(name)
		notices := strings.Count(out, "role-admin-bootstrap: temp_file_limit cannot be granted by this role; plugin roles run without it")
		if want := map[adminMode]int{adminSuperuser: 0, adminCreateRole: 1}[mode]; notices != want {
			t.Fatalf("bootstrap run %d printed the temp_file_limit notice %d times, want %d:\n%s", run, notices, want, out)
		}
	}
	c.exec(c.appURL(name), `DROP FUNCTION public.format(text, text)`)
	pool := c.migrate(name)
	needRestoreTools(t, pool)
	// A rerun after the core migrations read the core id too.
	c.bootstrap(name)

	var id8 string
	if err := pool.QueryRow(ctx, `SELECT left(id::text, 8) FROM hatamoto_instance`).Scan(&id8); err != nil {
		t.Fatal(err)
	}
	c.id8 = id8
	var adminExists, appCreateRole bool
	var extOwners int
	if err := c.super.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1),
		(SELECT rolcreaterole FROM pg_roles WHERE rolname = $2)`, "hatamoto_role_admin_"+id8, c.appRole).Scan(&adminExists, &appCreateRole); err != nil {
		t.Fatal(err)
	}
	if !adminExists || appCreateRole {
		t.Fatalf("role admin exists = %v, app role CREATEROLE = %v", adminExists, appCreateRole)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_extension e JOIN pg_roles r ON r.oid = e.extowner
		WHERE e.extname IN ('citext', 'uuid-ossp') AND r.rolname = $1`, c.appRole).Scan(&extOwners); err != nil || extOwners != 2 {
		t.Fatalf("extensions owned by the app role = %d, %v", extOwners, err)
	}

	// Seed: an entity, off-site backups on, a plugin with rows.
	var entityID string
	if err := pool.QueryRow(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, published_at)
		SELECT id, 'Restored Person', 'restored-person', 'published', jsonb_build_object('public', true), now()
		FROM hatamoto_entity_schemas WHERE slug = 'person' RETURNING id::text`).Scan(&entityID); err != nil {
		t.Fatalf("seeding an entity: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_offsite_settings (enabled, schedule) VALUES (true, 'daily')`); err != nil {
		t.Fatalf("seeding off-site settings: %v", err)
	}
	pdb := pluginhost.NewPluginDB(pool, quiet())
	if err := pdb.Provision(ctx, "webhooks"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if _, err := pdb.Migrate(ctx, "webhooks", []pluginhost.Migration{{Version: 1, Name: "1.sql",
		Stmts: []string{`CREATE TABLE deliveries (id int PRIMARY KEY, url text)`, `INSERT INTO deliveries VALUES (1, 'a'), (2, 'b')`}}}); err != nil {
		t.Fatalf("plugin migrate: %v", err)
	}
	var appRoleOfPlugin bool
	if err := c.super.QueryRow(ctx, `SELECT rolcreaterole OR rolsuper FROM pg_roles WHERE rolname = $1`, "hm_p_"+id8+"_webhooks").Scan(&appRoleOfPlugin); err != nil || appRoleOfPlugin {
		t.Fatalf("plugin role privileged = %v, %v", appRoleOfPlugin, err)
	}

	// Backup, then drop the site and the roles it made: a clean cluster.
	data, secretsDir, cfgPath := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "hatamoto.env")
	if err := os.WriteFile(filepath.Join(secretsDir, "secrets.env"), []byte("HATAMOTO_SECRET=abc123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("DATABASE_URL=x\nHATAMOTO_TRUSTED_KEYS=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := New(Options{Data: data, MediaDir: t.TempDir(), DatabaseURL: c.appURL(name), Pool: pool, Version: restoreVersion, Log: quiet(),
		SecretsDir: secretsDir, ConfigPath: cfgPath})
	info, err := e.Create(ctx, Request{Kind: KindManual})
	if err != nil || info.State != StateOK {
		t.Fatalf("Create: %+v, %v", info, err)
	}
	archive := filepath.Join(data, "backups", info.ID+".tar")
	pdb.Close()
	pool.Close()
	for _, q := range []string{
		"DROP DATABASE " + name + " WITH (FORCE)",
		"DROP ROLE hm_p_" + id8 + "_webhooks",
		"REVOKE SET ON PARAMETER temp_file_limit FROM hatamoto_role_admin_" + id8,
		"DROP ROLE hatamoto_role_admin_" + id8,
	} {
		if _, err := c.super.Exec(ctx, q); err != nil && !(mode == adminCreateRole && strings.HasPrefix(q, "REVOKE")) {
			t.Fatalf("destroy %q: %v", q, err)
		}
	}

	// Restore into a new database, as the admin role of this mode.
	target := "hm_d8_t_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	c.provisionApp(target)
	base := t.TempDir()
	o := RestoreOptions{
		Data: filepath.Join(base, "data"), MediaDir: filepath.Join(base, "uploads"), SecretsDir: filepath.Join(base, "secrets"),
		DatabaseURL: c.appURL(target), Version: restoreVersion, Log: quiet(),
	}
	if mode == adminCreateRole {
		c.negativeAdmins(t, archive, o, base)
	}
	if _, err := Restore(ctx, o, RestoreRequest{Source: archive, AdminURL: c.adminURL("postgres"), ConfigOut: filepath.Join(base, "hatamoto.env")}); err != nil {
		t.Fatalf("Restore as %s: %v", mode, err)
	}

	restored, err := pgxpool.New(ctx, c.appURL(target))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var entity string
	if err := restored.QueryRow(ctx, `SELECT name FROM hatamoto_entities WHERE id::text = $1`, entityID).Scan(&entity); err != nil || entity != "Restored Person" {
		t.Errorf("entity = %q, %v", entity, err)
	}
	var offsite bool
	if err := restored.QueryRow(ctx, `SELECT enabled FROM hatamoto_offsite_settings`).Scan(&offsite); err != nil || offsite {
		t.Errorf("off-site backups enabled after restore = %v, %v", offsite, err)
	}
	conn, err := restored.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{"hm_p_" + id8 + "_webhooks"}.Sanitize()); err != nil {
		t.Fatalf("set role: %v", err)
	}
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM plugin_webhooks.deliveries`).Scan(&n); err != nil || n != 2 {
		t.Errorf("plugin rows = %d, %v", n, err)
	}
}

// negativeAdmins: roles that are not enough as --admin-url are still refused
// by the dry run's checks on the real server.
func (c *d8Cluster) negativeAdmins(t *testing.T, archive string, o RestoreOptions, base string) {
	t.Helper()
	u := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	noDB, noCreate := "hm_d8_nodb_"+u, "hm_d8_nocr_"+u
	c.makeRole(noDB, "CREATEROLE NOCREATEDB")               // no CREATEDB
	c.makeRole(noCreate, "CREATEROLE CREATEDB NOSUPERUSER") // not granted CREATE on the target
	noInherit := "hm_d8_noinh_" + u                         // CREATE on the target, but does not inherit the app role
	c.makeRole(noInherit, "CREATEROLE CREATEDB NOSUPERUSER")
	db := o.DatabaseURL[strings.LastIndex(o.DatabaseURL, "/")+1:]
	if i := strings.IndexByte(db, '?'); i >= 0 {
		db = db[:i]
	}
	c.exec(c.url(c.super.Config().User, "postgres"), "GRANT "+c.appRole+" TO "+noInherit+" WITH SET TRUE, INHERIT FALSE")
	c.exec(o.DatabaseURL, "GRANT CREATE ON DATABASE "+db+" TO "+noInherit)
	for _, tc := range []struct{ role, want string }{
		{noInherit, "must inherit the application role"},
		{noDB, "--admin-url needs a superuser or a role with CREATEROLE and CREATEDB"},
		{noCreate, "needs CREATE on the target database"},
	} {
		_, err := Restore(context.Background(), o, RestoreRequest{Source: archive, AdminURL: c.url(tc.role, "postgres"), DryRun: true,
			ConfigOut: filepath.Join(base, "dry.env")})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("admin %s: err = %v, want %q", tc.role, err, tc.want)
		}
	}
}

// A role that holds CREATE on the database only without the grant option (or a
// membership that does not inherit) is refused before anything is changed:
// the script's reruns need the option. With the option it runs and reruns.
func TestBootstrapNeedsGrantOptionOnDatabaseCreate(t *testing.T) {
	c := newD8Cluster(t, adminSuperuser)
	u := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	adm, db := "hm_d8_ni_"+u, "hm_d8_ni_db_"+u
	c.makeRole(c.appRole, "NOCREATEROLE")
	c.makeRole(adm, "CREATEROLE CREATEDB NOSUPERUSER NOINHERIT")
	c.exec(c.url(c.super.Config().User, "postgres"),
		"GRANT "+c.appRole+" TO "+adm+" WITH SET TRUE, INHERIT FALSE",
		"CREATE DATABASE "+db+" OWNER "+c.appRole)
	c.dbs = append(c.dbs, db)
	c.exec(c.appURL(db), "GRANT CREATE ON DATABASE "+db+" TO "+adm)

	out, err := c.bootstrapAs(db, adm)
	if err == nil || !strings.Contains(out, "WITH GRANT OPTION") {
		t.Fatalf("without the grant option: err = %v\n%s", err, out)
	}
	c.exec(c.appURL(db), "GRANT CREATE ON DATABASE "+db+" TO "+adm+" WITH GRANT OPTION")
	for run := 1; run <= 2; run++ {
		if out, err := c.bootstrapAs(db, adm); err != nil {
			t.Fatalf("run %d with the grant option: %v\n%s", run, err, out)
		}
	}
	conn, err := pgx.Connect(context.Background(), c.url(c.super.Config().User, db))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if err := conn.QueryRow(context.Background(), `SELECT left(id, 8) FROM hatamoto_admin.instance`).Scan(&c.id8); err != nil {
		t.Fatal(err)
	}
}
