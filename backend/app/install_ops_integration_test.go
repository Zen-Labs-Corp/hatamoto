//go:build integration

package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/user"
)

func TestInstallOps_Integration(t *testing.T) {
	dsn := scratchDSN(t)
	t.Setenv("DATABASE_URL", dsn)
	ctx := database.WithSuperadmin(context.Background())
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrateUp(ctx, db.Pool(), quietLogger()); err != nil {
		t.Fatal(err)
	}
	logger := quietLogger()
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_api_keys WHERE name = 'ci-key'`)
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_users WHERE email = 'owner-ops@example.test'`)
	})

	// No owner yet: refused, and no file is left behind.
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "k")
	var out, info bytes.Buffer
	args := []string{"--name", "ci-key", "--scopes", "read:entities,write:entities", "--out", keyFile}
	var existing int
	_ = db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_users WHERE role='owner'`).Scan(&existing)
	if existing == 0 {
		if err := runAPIKeyCreate(ctx, args, logger, &out, &info); err == nil {
			t.Fatal("created a key without an owner")
		}
		if _, err := os.Stat(keyFile); err == nil {
			t.Fatal("a failed create left the key file")
		}
		if _, err := user.NewPostgresRepository(db).Create(ctx, &user.CreateUserInput{Email: "owner-ops@example.test", Role: user.RoleOwner}, "hash"); err != nil {
			t.Fatal(err)
		}
	}

	// Bad scope: nothing created, no file.
	if err := runAPIKeyCreate(ctx, []string{"--name", "ci-key", "--scopes", "nope", "--out", keyFile}, logger, &out, &info); err == nil || !strings.Contains(err.Error(), "unknown scope") {
		t.Fatalf("bad scope: %v", err)
	}
	if _, err := os.Stat(keyFile); err == nil {
		t.Fatal("a refused scope left the key file")
	}

	// --out: key only in the file, mode 0600, never on stdout; no overwrite.
	if err := runAPIKeyCreate(ctx, args, logger, &out, &info); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(keyFile)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", st, err)
	}
	b, _ := os.ReadFile(keyFile)
	key := strings.TrimSpace(string(b))
	if !strings.HasPrefix(key, "nxk_") || strings.Contains(out.String(), key) || strings.Contains(info.String(), key) {
		t.Fatalf("key handling: file %q, out %q", key, out.String())
	}
	if err := runAPIKeyCreate(ctx, args, logger, &out, &info); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("overwrite: %v", err)
	}
	// The key is stored hashed and acts as an owner; the audit row has no secret.
	var n int
	_ = db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_api_keys WHERE name='ci-key' AND created_by IS NOT NULL`).Scan(&n)
	var leaked int
	_ = db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_admin_audit WHERE action='apikey.create' AND detail::text LIKE '%'||$1||'%'`, key).Scan(&leaked)
	if n != 1 || leaked != 0 {
		t.Fatalf("stored keys %d, audit rows containing the key %d", n, leaked)
	}

	// Without --out: the key alone on stdout.
	out.Reset()
	info.Reset()
	if err := runAPIKeyCreate(ctx, []string{"--name", "ci-key", "--scopes", "*"}, logger, &out, &info); err != nil {
		t.Fatal(err)
	}
	if k := strings.TrimSpace(out.String()); !strings.HasPrefix(k, "nxk_") || strings.Contains(info.String(), k) {
		t.Fatalf("stdout %q info %q", out.String(), info.String())
	}

	// package list on an empty install; theme activate of an unknown theme fails; plugin settings of a missing plugin fails.
	out.Reset()
	if err := runPackageList(ctx, nil, &out); err != nil || !strings.Contains(out.String(), "no packages") {
		t.Fatalf("package list: %v %q", err, out.String())
	}
	if err := runThemeActivate(ctx, []string{"ghost"}, logger, &out); err == nil {
		t.Fatal("activated a theme that is not installed")
	}
	if err := runPluginSettingsSet(ctx, []string{"ghost", "a=b"}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("settings of a missing plugin: %v", err)
	}
	// default theme activation succeeds and is audited.
	if err := runThemeActivate(ctx, []string{"default"}, logger, &out); err != nil {
		t.Fatal(err)
	}
	_ = db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_admin_audit WHERE action='package.activate' AND actor='cli'`).Scan(&n)
	if n == 0 {
		t.Fatal("activation not audited")
	}
	_ = db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_admin_audit WHERE action='apikey.create' AND actor='cli' AND detail->>'name'='ci-key'`).Scan(&n)
	if n != 2 {
		t.Fatalf("apikey.create audit rows = %d, want 2", n)
	}
}

// A theme that calls a plugin's template func, partial and section activates
// from the CLI when the plugin is installed, as it does in the admin; without
// the plugin it does not.
func TestThemeActivateWithPluginTemplates_Integration(t *testing.T) {
	dsn := scratchDSN(t)
	t.Setenv("DATABASE_URL", dsn)
	ctx := database.WithSuperadmin(context.Background())
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrateUp(ctx, db.Pool(), quietLogger()); err != nil {
		t.Fatal(err)
	}
	st := packages.NewStore(db.Pool())
	put := func(dir string) {
		raw, err := packages.PackDir(dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		p, err := packages.Parse(raw, packages.DefaultLimits)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Put(ctx, p, packages.SourceUpload, true); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = st.SetActiveTheme(ctx, "")
		_ = st.Delete(ctx, packages.KindTheme, "tplcall")
		_ = st.Delete(ctx, packages.KindPlugin, "tplplug")
	})
	put(writeDir(t, map[string]string{
		"theme.json":        `{"name":"tplcall","version":"1.0.0"}`,
		"layouts/base.html": `{{ define "base" }}<html><body>{{ plugin_tplplug_hello "x" }}{{ template "plugin/tplplug/card" . }}</body></html>{{ end }}`,
	}))
	var out bytes.Buffer
	if err := runThemeActivate(ctx, []string{"tplcall", "--force"}, quietLogger(), &out); err == nil {
		t.Fatal("activated a theme calling a func no installed plugin provides")
	}
	put(writeDir(t, map[string]string{
		"plugin.json":          `{"name":"tplplug","version":"1.0.0","sdk":"^0.7","funcs":["hello"]}`,
		"plugin.wasm":          "\x00asm",
		"partials/card.html":   `<p>card</p>`,
		"sections/tplsec.html": `{{define "section-tplsec"}}<div>sec</div>{{end}}`,
	}))
	if err := runThemeActivate(ctx, []string{"tplcall", "--force"}, quietLogger(), &out); err != nil {
		t.Fatalf("activate with the plugin installed: %v", err)
	}
}
