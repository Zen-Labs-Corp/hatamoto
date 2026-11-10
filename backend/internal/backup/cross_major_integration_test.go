//go:build integration

package backup

import (
	"context"
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

	"hatamoto.dev/core/internal/pluginhost"
)

// A backup made by an older PostgreSQL major restores into a newer server when
// pg_restore and psql match the server. The source is a second, older cluster
// (HATAMOTO_TEST_PG_SOURCE_URL, a superuser DSN) with its tools in
// HATAMOTO_TEST_PG_SOURCE_BIN; the target is DATABASE_URL, with the newer
// tools first on PATH. The test skips when that setup is not there.

var versionMajor = regexp.MustCompile(`(\d+)(?:\.\d+)?`)

func toolMajorOf(t *testing.T, bin string, args ...string) int {
	t.Helper()
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		t.Skipf("%s not usable: %v", bin, err)
	}
	m := versionMajor.FindStringSubmatch(string(out))
	if m == nil {
		t.Skipf("%s --version printed no version: %q", bin, out)
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func serverMajorOf(t *testing.T, c *d8Cluster) int {
	t.Helper()
	var n int
	if err := c.super.QueryRow(context.Background(), `SELECT current_setting('server_version_num')::int / 10000`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCrossMajorRestoreOlderIntoNewer(t *testing.T) {
	for _, mode := range []adminMode{adminSuperuser, adminCreateRole} {
		t.Run(mode.String(), func(t *testing.T) { crossMajorFlow(t, mode) })
	}
}

func crossMajorFlow(t *testing.T, mode adminMode) {
	ctx := context.Background()
	srcBin := os.Getenv("HATAMOTO_TEST_PG_SOURCE_BIN")
	if srcBin == "" {
		t.Skip("HATAMOTO_TEST_PG_SOURCE_BIN not set")
	}
	src := newD8ClusterDSN(t, adminSuperuser, os.Getenv("HATAMOTO_TEST_PG_SOURCE_URL"), "HATAMOTO_TEST_PG_SOURCE_URL not set")
	dst := newD8Cluster(t, mode)
	srcMajor, dstMajor := serverMajorOf(t, src), serverMajorOf(t, dst)
	if srcMajor >= dstMajor {
		t.Skipf("source server %d is not older than the target %d", srcMajor, dstMajor)
	}
	if n := toolMajorOf(t, filepath.Join(srcBin, "pg_dump"), "--version"); n != srcMajor {
		t.Skipf("source pg_dump %d does not match the source server %d", n, srcMajor)
	}
	for _, tool := range []string{"pg_restore", "psql"} {
		if n := toolMajorOf(t, tool, "--version"); n != dstMajor {
			t.Skipf("%s %d does not match the target server %d", tool, n, dstMajor)
		}
	}

	// The source site: migrated, seeded, backed up with the source's pg_dump.
	name := "hm_xm_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	src.provisionApp(name)
	src.bootstrap(name)
	pool := src.migrate(name)
	src.bootstrap(name)
	var id8, entityID string
	if err := pool.QueryRow(ctx, `SELECT left(id::text, 8) FROM hatamoto_instance`).Scan(&id8); err != nil {
		t.Fatal(err)
	}
	src.id8 = id8
	if err := pool.QueryRow(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, published_at)
		SELECT id, 'Restored Person', 'restored-person', 'published', jsonb_build_object('public', true), now()
		FROM hatamoto_entity_schemas WHERE slug = 'person' RETURNING id::text`).Scan(&entityID); err != nil {
		t.Fatalf("seeding an entity: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_offsite_settings (enabled, schedule) VALUES (true, 'daily')`); err != nil {
		t.Fatalf("seeding off-site settings: %v", err)
	}
	pdb := pluginhost.NewPluginDB(pool, quiet())
	defer pdb.Close()
	if err := pdb.Provision(ctx, "webhooks"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if _, err := pdb.Migrate(ctx, "webhooks", []pluginhost.Migration{{Version: 1, Name: "1.sql",
		Stmts: []string{`CREATE TABLE deliveries (id int PRIMARY KEY, url text)`, `INSERT INTO deliveries VALUES (1, 'a'), (2, 'b')`}}}); err != nil {
		t.Fatalf("plugin migrate: %v", err)
	}

	data, secretsDir, cfgPath := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "hatamoto.env")
	if err := os.WriteFile(filepath.Join(secretsDir, "secrets.env"), []byte("HATAMOTO_SECRET=abc123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("DATABASE_URL=x\nHATAMOTO_TRUSTED_KEYS=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fromSource := func(ctx context.Context, env []string, tool string, args ...string) ([]byte, error) {
		if tool == "pg_dump" {
			tool = filepath.Join(srcBin, tool)
		}
		return OSExec(ctx, env, tool, args...)
	}
	e := New(Options{Data: data, MediaDir: t.TempDir(), DatabaseURL: src.appURL(name), Pool: pool, Version: restoreVersion, Log: quiet(),
		SecretsDir: secretsDir, ConfigPath: cfgPath, Run: fromSource})
	info, err := e.Create(ctx, Request{Kind: KindManual})
	if err != nil || info.State != StateOK {
		t.Fatalf("Create: %+v, %v", info, err)
	}
	archive := filepath.Join(data, "backups", info.ID+".tar")
	_, body := readTar(t, archive)
	if mf := readManifest(t, body); mf.ServerMajor != srcMajor {
		t.Fatalf("the backup records PostgreSQL %d, want the source's %d", mf.ServerMajor, srcMajor)
	}

	// Restore into the newer cluster, as the admin role of this mode.
	dst.id8 = id8
	dst.appRole = src.appRole // the dump names its owner, so the target needs the same role
	target := "hm_xm_t_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	dst.provisionApp(target)
	base := t.TempDir()
	o := RestoreOptions{
		Data: filepath.Join(base, "data"), MediaDir: filepath.Join(base, "uploads"), SecretsDir: filepath.Join(base, "secrets"),
		DatabaseURL: dst.appURL(target), Version: restoreVersion, Log: quiet(),
	}
	if _, err := Restore(ctx, o, RestoreRequest{Source: archive, AdminURL: dst.adminURL("postgres"), ConfigOut: filepath.Join(base, "hatamoto.env")}); err != nil {
		t.Fatalf("Restore PostgreSQL %d backup into %d as %s: %v", srcMajor, dstMajor, mode, err)
	}

	restored, err := pgxpool.New(ctx, dst.appURL(target))
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
