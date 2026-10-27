//go:build integration

package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/pluginhost"
)

// These tests run create, destroy and restore against a real server. They need
// what the engine tests need (a superuser DATABASE_URL on PostgreSQL N) plus
// pg_dump, pg_restore and psql of major N on PATH; they skip otherwise.

const restoreVersion = "test-restore"

var (
	mediaBytes = []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 3, 0xff, 0xfe}
	mediaKey   = filepath.Join("2026", "10", "photo.png")
	mediaURL   = "/media/" + "2026/10/photo.png"
)

// destroyed is a backup of a seeded site whose database and cluster roles are gone.
type destroyed struct {
	archive  string
	appURL   string // the app role on the source database (now dropped)
	adminURL string // superuser, any database
	appRole  string
	id8      string
	entityID string
	secrets  []byte
	config   []byte
	admin    *pgx.Conn
}

func needRestoreTools(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	needMatchingPgDump(t, pool)
	var server int
	if err := pool.QueryRow(context.Background(), `SELECT current_setting('server_version_num')::int / 10000`).Scan(&server); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"pg_restore", "psql"} {
		out, err := exec.Command(tool, "--version").CombinedOutput()
		if err != nil {
			t.Skipf("%s not usable: %v", tool, err)
		}
		if n, _ := strconv.Atoi(regexp.MustCompile(`(\d+)`).FindString(string(out))); n != server {
			t.Skipf("%s %d does not match server %d", tool, n, server)
		}
	}
}

func withDatabase(t *testing.T, raw, db string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + db
	return u.String()
}

// seedAndDestroy seeds a site (published entity, media file, plugin rows,
// secrets.env, config), takes a backup, then drops the database and every
// cluster role the site made, so the cluster looks like a clean machine that
// has only the app role.
func seedAndDestroy(t *testing.T) *destroyed { return seedAndDestroyWith(t, nil) }

// seedAndDestroyWith is seedAndDestroy with a hook that changes the source
// database after seeding and before the backup is taken.
func seedAndDestroyWith(t *testing.T, mutate func(ctx context.Context, pool *pgxpool.Pool)) *destroyed {
	t.Helper()
	ctx := context.Background()
	pool, appURL := scratchDB(t, modeBootstrap)
	needRestoreTools(t, pool)
	d := &destroyed{appURL: appURL, adminURL: os.Getenv("DATABASE_URL"), appRole: "hm_bk_lowapp"}

	if err := pool.QueryRow(ctx, `SELECT left(id::text, 8) FROM hatamoto_instance`).Scan(&d.id8); err != nil {
		t.Fatal(err)
	}
	// A published entity.
	if err := pool.QueryRow(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, published_at)
		SELECT id, 'Restored Person', 'restored-person', 'published', jsonb_build_object('public', true), now()
		FROM hatamoto_entity_schemas WHERE slug = 'person' RETURNING id::text`).Scan(&d.entityID); err != nil {
		t.Fatalf("seeding an entity: %v", err)
	}
	// A media file with a public URL.
	media := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(media, mediaKey)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(media, mediaKey), mediaBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_media (filename, original_filename, mime_type, file_size, storage_path, public_url)
		VALUES ('photo.png', 'photo.png', 'image/png', $1, $2, $3)`, len(mediaBytes), mediaKey, mediaURL); err != nil {
		t.Fatalf("seeding media: %v", err)
	}
	// A plugin with rows in its own schema.
	pdb := pluginhost.NewPluginDB(pool, quiet())
	t.Cleanup(pdb.Close)
	if err := pdb.Provision(ctx, "webhooks"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if _, err := pdb.Migrate(ctx, "webhooks", []pluginhost.Migration{{Version: 1, Name: "1.sql",
		Stmts: []string{`CREATE TABLE deliveries (id int PRIMARY KEY, url text)`, `INSERT INTO deliveries VALUES (1, 'a'), (2, 'b')`}}}); err != nil {
		t.Fatalf("plugin migrate: %v", err)
	}

	if mutate != nil {
		mutate(ctx, pool)
	}

	secretsDir, cfgDir := t.TempDir(), t.TempDir()
	d.secrets, d.config = []byte("HATAMOTO_SECRET=abc123\n"), []byte("DATABASE_URL=x\nHATAMOTO_TRUSTED_KEYS=\n")
	if err := os.WriteFile(filepath.Join(secretsDir, "secrets.env"), d.secrets, 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "hatamoto.env")
	if err := os.WriteFile(cfgPath, d.config, 0o600); err != nil {
		t.Fatal(err)
	}

	data := t.TempDir()
	e := New(Options{Data: data, MediaDir: media, DatabaseURL: appURL, Pool: pool, Version: restoreVersion, Log: quiet(),
		SecretsDir: secretsDir, ConfigPath: cfgPath})
	info, err := e.Create(ctx, Request{Kind: KindManual})
	if err != nil || info.State != StateOK {
		t.Fatalf("Create: %+v, %v", info, err)
	}
	d.archive = filepath.Join(data, "backups", info.ID+".tar")

	// Destroy: the database, the plugin role, the admin role. The app role stays.
	admin, err := pgx.Connect(ctx, d.adminURL)
	if err != nil {
		t.Fatal(err)
	}
	d.admin = admin
	t.Cleanup(func() { admin.Close(ctx) })
	pdb.Close()
	pool.Close()
	source := strings.TrimPrefix(mustParse(t, appURL).Path, "/")
	adminRole := pgx.Identifier{"hatamoto_role_admin_" + d.id8}.Sanitize()
	pluginRole := pgx.Identifier{"hm_p_" + d.id8 + "_webhooks"}.Sanitize()
	for _, q := range []string{
		"DROP DATABASE " + source + " WITH (FORCE)",
		"DROP ROLE " + pluginRole,
		"REVOKE SET ON PARAMETER temp_file_limit FROM " + adminRole,
		"DROP ROLE " + adminRole,
	} {
		if _, err := admin.Exec(ctx, q); err != nil {
			t.Fatalf("destroy %q: %v", q, err)
		}
	}
	return d
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// freshTarget creates an empty database owned by the app role and removes it
// (before the scratch cleanup drops the roles) when the test ends.
func (d *destroyed) freshTarget(t *testing.T, name string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := d.admin.Exec(ctx, "CREATE DATABASE "+name+" OWNER "+d.appRole); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)") })
	return withDatabase(t, d.appURL, name)
}

func (d *destroyed) options(t *testing.T, targetURL string) (RestoreOptions, string, string, string) {
	t.Helper()
	base := t.TempDir()
	o := RestoreOptions{
		Data: filepath.Join(base, "data"), MediaDir: filepath.Join(base, "uploads"), SecretsDir: filepath.Join(base, "secrets"),
		DatabaseURL: targetURL, Version: restoreVersion, Log: quiet(),
	}
	return o, o.MediaDir, o.SecretsDir, filepath.Join(base, "hatamoto.env")
}

func TestRestoreIntegrationRoundTrip(t *testing.T) {
	ctx := context.Background()
	d := seedAndDestroy(t)
	target := d.freshTarget(t, "hm_bk_restored")
	o, mediaDir, secretsDir, configOut := d.options(t, target)

	plan, err := Restore(ctx, o, RestoreRequest{Source: d.archive, AdminURL: d.adminURL, ConfigOut: configOut})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	var out bytes.Buffer
	plan.Print(&out)
	t.Logf("\n%s", out.String())
	if !plan.Done || !strings.Contains(out.String(), "restore complete") {
		t.Errorf("output:\n%s", out.String())
	}

	pool, err := pgxpool.New(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// ISC-12: the published entity reads back.
	var name, status string
	if err := pool.QueryRow(ctx, `SELECT name, status FROM hatamoto_entities WHERE id::text = $1`, d.entityID).Scan(&name, &status); err != nil {
		t.Fatalf("entity: %v", err)
	}
	if name != "Restored Person" || status != "published" {
		t.Errorf("entity = %q %q", name, status)
	}

	// ISC-13: media bytes, public_url and storage_path are identical.
	got, err := os.ReadFile(filepath.Join(mediaDir, mediaKey))
	if err != nil || !bytes.Equal(got, mediaBytes) {
		t.Errorf("media file = %v, %v", got, err)
	}
	var storagePath, publicURL string
	if err := pool.QueryRow(ctx, `SELECT storage_path, public_url FROM hatamoto_media WHERE filename = 'photo.png'`).Scan(&storagePath, &publicURL); err != nil {
		t.Fatal(err)
	}
	if storagePath != mediaKey || publicURL != mediaURL {
		t.Errorf("media row = %q %q", storagePath, publicURL)
	}

	// ISC-14: the plugin rows read as the plugin role, reached by the app role.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{"hm_p_" + d.id8 + "_webhooks"}.Sanitize()); err != nil {
		t.Fatalf("set role: %v", err)
	}
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM plugin_webhooks.deliveries`).Scan(&n); err != nil || n != 2 {
		t.Errorf("plugin rows = %d, %v", n, err)
	}

	// Secrets and config, 0600 and verbatim.
	for path, want := range map[string][]byte{filepath.Join(secretsDir, "secrets.env"): d.secrets, configOut: d.config} {
		b, err := os.ReadFile(path)
		fi, serr := os.Stat(path)
		if err != nil || serr != nil || !bytes.Equal(b, want) || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s = %q, %v %v", path, b, err, serr)
		}
	}

	// ISC-15: the manifest's trust IDs are this binary's embedded IDs.
	want, _ := embeddedTrust()
	if got := archiveTrust(t, d.archive); got == nil || !reflect.DeepEqual(got.Release, want.Release) || !reflect.DeepEqual(got.Manifest, want.Manifest) {
		t.Errorf("manifest trust = %+v, want %+v", got, want)
	}
}

func archiveTrust(t *testing.T, archive string) *TrustIDs {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			t.Fatal("no manifest in the archive")
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name != manifestName {
			continue
		}
		var mf manifest
		if err := json.NewDecoder(tr).Decode(&mf); err != nil {
			t.Fatal(err)
		}
		return mf.Trust
	}
}

// ISC-16: a dry run runs every check against the real server and writes nothing.
func TestRestoreIntegrationDryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	d := seedAndDestroy(t)
	target := d.freshTarget(t, "hm_bk_dry")
	o, mediaDir, secretsDir, configOut := d.options(t, target)

	plan, err := Restore(ctx, o, RestoreRequest{Source: d.archive, AdminURL: d.adminURL, ConfigOut: configOut, DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	var out bytes.Buffer
	plan.Print(&out)
	if !strings.Contains(out.String(), "dry run: nothing written") || strings.Contains(out.String(), "restore complete") {
		t.Errorf("output:\n%s", out.String())
	}

	pool, err := pgxpool.New(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	rels, err := nonEmptySchemas(ctx, pool)
	if err != nil || len(rels) != 0 {
		t.Errorf("target has relations in %v, %v", rels, err)
	}
	for _, p := range []string{o.Data, mediaDir, secretsDir, configOut} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s exists after a dry run", p)
		}
	}
}

// A dump that carries public.hatamoto_instance as a view would make the
// superuser bootstrap run the view's query: the guard stops the restore at the
// bootstrap, and the app role never becomes a superuser.
func TestRestoreIntegrationGuardStopsPlantedInstanceView(t *testing.T) {
	ctx := context.Background()
	d := seedAndDestroyWith(t, func(ctx context.Context, pool *pgxpool.Pool) {
		for _, q := range []string{
			`ALTER TABLE hatamoto_instance RENAME TO hatamoto_instance_real`,
			`CREATE VIEW hatamoto_instance AS SELECT id FROM hatamoto_instance_real`,
		} {
			if _, err := pool.Exec(ctx, q); err != nil {
				t.Fatalf("planting the view %q: %v", q, err)
			}
		}
	})
	target := d.freshTarget(t, "hm_bk_planted")
	o, _, _, configOut := d.options(t, target)

	_, err := Restore(ctx, o, RestoreRequest{Source: d.archive, AdminURL: d.adminURL, ConfigOut: configOut})
	if err == nil || !strings.Contains(err.Error(), "restore stopped at role bootstrap") || !strings.Contains(err.Error(), "not a plain table") {
		t.Fatalf("Restore error = %v", err)
	}
	var super bool
	if err := d.admin.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = $1`, d.appRole).Scan(&super); err != nil || super {
		t.Errorf("app role superuser = %v, %v", super, err)
	}
}
