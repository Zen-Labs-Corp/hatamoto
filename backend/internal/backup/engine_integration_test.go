//go:build integration

package backup

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// These tests need a Postgres superuser URL in DATABASE_URL and a pg_dump of
// the server's major version on PATH (they skip otherwise). The application
// role is a plain role, never a superuser, so the plugin isolation of
// production holds: the plugin role is NOINHERIT for the app role, which can
// only reach the schema with SET ROLE.

type roleMode int

const (
	modeCreateRole roleMode = iota // direct CREATE ROLE: the app role has CREATEROLE
	modeBootstrap                  // no extra attribute; role-admin-bootstrap.sql ran as superuser
)

func (m roleMode) String() string { return [...]string{"createrole", "bootstrap"}[m] }

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// scratchDB creates a migrated database owned by a plain application role and
// returns the application pool and the application URL (for pg_dump).
func scratchDB(t *testing.T, mode roleMode) (*pgxpool.Pool, string) {
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
	if !super {
		admin.Close(ctx)
		t.Skip("these tests need a superuser DATABASE_URL")
	}
	if mode == modeBootstrap {
		if _, err := exec.LookPath("psql"); err != nil {
			admin.Close(ctx)
			t.Skip("psql not installed")
		}
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_bk_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	appRole, attr := "hm_bk_app", "CREATEROLE"
	if mode == modeBootstrap {
		appRole, attr = "hm_bk_lowapp", "NOCREATEROLE"
	}
	var exists bool
	_ = admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, appRole).Scan(&exists)
	if !exists {
		if _, err := admin.Exec(ctx, `CREATE ROLE `+appRole+` LOGIN PASSWORD 'x' `+attr); err != nil {
			t.Fatal(err)
		}
	}
	superCfg := admin.Config().Copy()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name+" OWNER "+appRole); err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.User, cfg.ConnConfig.Password, cfg.ConnConfig.Database = appRole, "x", name
	if mode == modeBootstrap {
		cmd := exec.Command("psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "-v", "app_role="+appRole, "-d", name,
			"-f", "../../../deploy/sql/role-admin-bootstrap.sql")
		cmd.Env = append(os.Environ(), "PGHOST="+superCfg.Host, "PGPORT="+strconv.Itoa(int(superCfg.Port)),
			"PGUSER="+superCfg.User, "PGPASSWORD="+superCfg.Password)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bootstrap: %v\n%s", err, out)
		}
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var dbID string
	t.Cleanup(func() {
		pool.Close()
		var roles []string
		if dbID != "" {
			rows, _ := admin.Query(ctx, `SELECT rolname FROM pg_roles WHERE rolname LIKE $1`, "hm\\_p\\_"+dbID[:8]+"\\_%")
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
			r := pgx.Identifier{"hatamoto_role_admin_" + dbID[:8]}.Sanitize()
			_, _ = admin.Exec(ctx, "REVOKE SET ON PARAMETER temp_file_limit FROM "+r)
			_, _ = admin.Exec(ctx, "DROP ROLE IF EXISTS "+r)
		}
		_, _ = admin.Exec(ctx, "DROP ROLE IF EXISTS "+appRole)
		admin.Close(ctx)
	})
	prov, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prov.Up(ctx); err != nil {
		t.Fatalf("core migrations: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM hatamoto_instance`).Scan(&dbID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_users (id, email, password_hash, role) VALUES (gen_random_uuid(), 'owner@example.com', 'x', 'owner')`); err != nil {
		t.Fatalf("seeding a user: %v", err)
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword(appRole, "x"),
		Host: cfg.ConnConfig.Host + ":" + strconv.Itoa(int(cfg.ConnConfig.Port)), Path: "/" + name, RawQuery: "sslmode=disable"}
	return pool, u.String()
}

// needMatchingPgDump skips unless pg_dump on PATH has the server's major.
func needMatchingPgDump(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	out, err := exec.Command("pg_dump", "--version").CombinedOutput()
	if err != nil {
		t.Skipf("pg_dump not usable: %v", err)
	}
	m := regexp.MustCompile(`(\d+)`).FindStringSubmatch(string(out))
	var server int
	if err := pool.QueryRow(context.Background(), `SELECT current_setting('server_version_num')::int / 10000`).Scan(&server); err != nil {
		t.Fatal(err)
	}
	if tool, _ := strconv.Atoi(m[1]); tool != server {
		t.Skipf("pg_dump %d does not match server %d", tool, server)
	}
	if _, err := exec.LookPath("pg_restore"); err != nil {
		t.Skip("pg_restore not installed")
	}
	return string(out)
}

func TestEngineBacksUpNoInheritPlugin(t *testing.T) {
	for _, mode := range []roleMode{modeCreateRole, modeBootstrap} {
		t.Run(mode.String(), func(t *testing.T) { backsUpNoInheritPlugin(t, mode) })
	}
}

func backsUpNoInheritPlugin(t *testing.T, mode roleMode) {
	ctx := context.Background()
	pool, appURL := scratchDB(t, mode)
	needMatchingPgDump(t, pool)

	// A plugin provisioned like production, with a table and rows.
	pdb := pluginhost.NewPluginDB(pool, quiet())
	t.Cleanup(func() { pdb.Close() })
	if err := pdb.Provision(ctx, "webhooks"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if _, err := pdb.Migrate(ctx, "webhooks", []pluginhost.Migration{{Version: 1, Name: "1.sql",
		Stmts: []string{`CREATE TABLE deliveries (id int PRIMARY KEY, url text)`, `INSERT INTO deliveries VALUES (1, 'a'), (2, 'b')`}}}); err != nil {
		t.Fatalf("plugin migrate: %v", err)
	}

	// The app role cannot read the plugin schema as itself (NOINHERIT).
	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM plugin_webhooks.deliveries`).Scan(&n)
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("app role read the plugin schema as itself: n=%d err=%v", n, err)
	}

	// Negative control: a plain pg_dump as the app role fails, the bug the
	// engine exists to avoid.
	env, _ := pgEnv("DATABASE_URL", appURL, true)
	cmd := exec.Command("pg_dump", "--format=custom", "--file="+filepath.Join(t.TempDir(), "plain.dump"))
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "permission denied") {
		t.Fatalf("plain pg_dump as the app role must fail with permission denied: err=%v\n%s", err, out)
	}
	t.Logf("negative control: %s", ErrorLine(out))

	data := t.TempDir()
	e := New(Options{Data: data, MediaDir: t.TempDir(), DatabaseURL: appURL, Pool: pool, Version: "test"})
	info, err := e.Create(ctx, Request{Kind: KindManual})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if info.State != StateOK {
		t.Fatalf("state = %s (%s)", info.State, info.Error)
	}

	// The archive holds the four kinds of entry.
	dir := t.TempDir()
	f, err := os.Open(filepath.Join(data, "backups", info.ID+".tar"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got := map[string]bool{}
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got[h.Name] = true
		b, _ := io.ReadAll(tr)
		if err := os.WriteFile(filepath.Join(dir, h.Name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"core.dump", "plugin_webhooks.dump", "content.tar.gz", "manifest.json"} {
		if !got[want] {
			t.Errorf("archive lacks %s (has %v)", want, got)
		}
	}
	var mf manifest
	raw, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err := json.Unmarshal(raw, &mf); err != nil || mf.Format != "hatamoto-backup" || len(mf.Plugins) != 1 || mf.Plugins[0] != "webhooks" {
		t.Errorf("manifest = %+v, %v", mf, err)
	}

	list := func(file string) string {
		out, err := exec.Command("pg_restore", "-l", filepath.Join(dir, file)).CombinedOutput()
		if err != nil {
			t.Fatalf("pg_restore -l %s: %v\n%s", file, err, out)
		}
		return string(out)
	}
	plugin := list("plugin_webhooks.dump")
	if !regexp.MustCompile(`TABLE plugin_webhooks deliveries`).MatchString(plugin) {
		t.Errorf("plugin dump does not list the table:\n%s", plugin)
	}
	core := list("core.dump")
	// \b keeps the core table hatamoto_admin_audit from matching the schema.
	for _, bad := range []string{"hatamoto_admin", "plugin_webhooks"} {
		if regexp.MustCompile(`\b` + bad + `\b`).MatchString(core) {
			t.Errorf("core dump mentions %s:\n%s", bad, core)
		}
	}
	if !strings.Contains(core, "hatamoto_users") {
		t.Errorf("core dump lacks the core tables:\n%s", core)
	}
}

// The real content export, not a stub, reports a referenced file that is not on
// disk, and the engine keeps the backup and records the gap.
func TestEngineKeepsBackupWithMissingMedia(t *testing.T) {
	ctx := context.Background()
	pool, appURL := scratchDB(t, modeCreateRole)
	needMatchingPgDump(t, pool)

	media := t.TempDir()
	if err := os.WriteFile(filepath.Join(media, "present.txt"), []byte("here"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"present.txt", "gone.txt"} {
		if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_media (filename, original_filename, mime_type, file_size, storage_path)
			VALUES ($1, $1, 'text/plain', 4, $1)`, name); err != nil {
			t.Fatal(err)
		}
	}
	e := New(Options{Data: t.TempDir(), MediaDir: media, DatabaseURL: appURL, Pool: pool, Version: "test", Log: quiet()})
	info, err := e.Create(ctx, Request{Kind: KindManual})
	if err != nil || info.State != StateOK || info.MediaMissing != 1 {
		t.Fatalf("info = %+v err = %v", info, err)
	}

	// Every file gone: the backup fails.
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_media SET storage_path = 'x-' || storage_path`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Create(ctx, Request{Kind: KindManual}); err == nil || !strings.Contains(err.Error(), "none of the 2 media files") {
		t.Fatalf("err = %v", err)
	}
}
