//go:build integration

package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/webhook"
	"hatamoto.dev/core/migrations"
)

// legacyRig is a migrated scratch database whose app role may create the
// per-plugin roles, with the webhooks plugin installed and one subscription in
// its own schema, as a site upgrading from the plugin looks.
type legacyRig struct {
	s   *Server
	pdb *pluginhost.PluginDB
}

func newLegacyRig(t *testing.T) *legacyRig {
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
	_ = admin.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	owner := ""
	if super { // a superuser would bypass the role isolation the plugin relies on
		var exists bool
		_ = admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'hm_fp_app')`).Scan(&exists)
		if !exists {
			if _, err := admin.Exec(ctx, `CREATE ROLE hm_fp_app LOGIN PASSWORD 'x' CREATEROLE`); err != nil {
				t.Fatal(err)
			}
		}
		owner = " OWNER hm_fp_app"
		u.User = url.UserPassword("hm_fp_app", "x")
	}
	name := "hm_wl_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name+owner); err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := database.New(ctx, config.DatabaseConfig{URL: u.String(), MaxConnections: 8, MinConnections: 1})
	if err != nil {
		t.Fatal(err)
	}
	pool := db.Pool()
	t.Cleanup(func() {
		var like string
		_ = pool.QueryRow(ctx, `SELECT 'hm\_p\_' || left(id, 8) || '\_%' FROM hatamoto_instance`).Scan(&like)
		db.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		if like != "" {
			rows, _ := admin.Query(ctx, `SELECT rolname FROM pg_roles WHERE rolname LIKE $1`, like)
			var roles []string
			for rows != nil && rows.Next() {
				var r string
				_ = rows.Scan(&r)
				roles = append(roles, r)
			}
			if rows != nil {
				rows.Close()
			}
			for _, r := range roles {
				_, _ = admin.Exec(ctx, `DROP ROLE IF EXISTS "`+r+`"`)
			}
		}
		admin.Close(ctx)
	})

	sqlDB := stdlib.OpenDBFromPool(pool)
	prov, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prov.Up(ctx); err != nil {
		t.Fatal(err)
	}
	sqlDB.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pdb := pluginhost.NewPluginDB(pool, log)
	if err := pdb.Provision(ctx, "webhooks"); err != nil {
		t.Skipf("cannot provision a plugin role here: %v", err)
	}
	t.Cleanup(pdb.Close)
	if _, err := pdb.Migrate(ctx, "webhooks", []pluginhost.Migration{{Version: 1, Stmts: []string{
		`CREATE TABLE subscriptions (id text PRIMARY KEY, name text NOT NULL, url text NOT NULL, secret text NOT NULL,
			events jsonb NOT NULL DEFAULT '[]', headers jsonb NOT NULL DEFAULT '{}', active boolean NOT NULL DEFAULT true,
			max_retries integer NOT NULL DEFAULT 3, retry_delay_seconds integer NOT NULL DEFAULT 60,
			timeout_seconds integer NOT NULL DEFAULT 5, allow_http boolean NOT NULL DEFAULT false,
			created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now())`,
	}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := pdb.Exec(ctx, "webhooks", `INSERT INTO subscriptions (id, name, url, secret, events, headers, max_retries, timeout_seconds)
		VALUES ('11111111-1111-1111-1111-111111111111', 'Build', 'https://hooks.example.test/in', 'whsec_`+strings.Repeat("ab", 32)+`',
		'["entity.published","bogus"]', '{"X-Token":"t","Host":"evil"}', 99, 0)`); err != nil {
		t.Fatal(err)
	}
	fp, err := packages.ParsePublicKey(config.FirstPartyKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_packages (kind, name, version, manifest, data, sha256, signature, signer_key_id, source)
		VALUES ('plugin', 'webhooks', '0.1.0', '{"name":"webhooks","version":"0.1.0"}', '\x00', 'x', '\x00', $1, 'registry')`, packages.KeyID(fp)); err != nil {
		t.Fatal(err)
	}
	// An enabled plugin that was granted what it needs to send.
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_plugin_grants (plugin, permissions) VALUES ('webhooks', '{kv,db,http.fetch,http.fetch.any}')`); err != nil {
		t.Fatal(err)
	}
	// The boot step runs in a fresh process: a PluginDB with no provisioned state.
	pdb.Close()
	pdb = pluginhost.NewPluginDB(pool, log)
	t.Cleanup(pdb.Close)
	s := &Server{db: db, logger: log, pluginDB: pdb, webhooks: webhook.NewService(db, log)}
	s.packages = packages.NewManager(packages.Options{Pool: pool, Dropper: pdb, Logger: log, Trust: packages.TrustSet{packages.KeyID(fp): fp}})
	return &legacyRig{s: s, pdb: pdb}
}

func (r *legacyRig) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := r.s.db.Pool().QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWebhooksLegacyPluginIsCopiedAndUninstalled(t *testing.T) {
	r := newLegacyRig(t)
	ctx := context.Background()
	const id = "11111111-1111-1111-1111-111111111111"

	r.s.retireWebhooksPlugin(ctx)

	got, err := r.s.webhooks.Get(ctx, id)
	if err != nil {
		t.Fatalf("the subscription was not copied: %v", err)
	}
	if got.Secret != "whsec_"+strings.Repeat("ab", 32) || got.Name != "Build" || got.URL != "https://hooks.example.test/in" {
		t.Fatalf("copied row differs: %+v", got)
	}
	if len(got.Events) != 1 || got.Events[0] != "entity.published" || got.MaxRetries != 10 || got.TimeoutSeconds != 1 {
		t.Fatalf("row not sanitised: %+v", got)
	}
	if _, bad := got.Headers["Host"]; bad || got.Headers["X-Token"] != "t" {
		t.Fatalf("headers not sanitised: %v", got.Headers)
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_packages WHERE name = 'webhooks'`); n != 0 {
		t.Fatalf("package row still there: %d", n)
	}
	if n := r.count(t, `SELECT count(*) FROM pg_namespace WHERE nspname = 'plugin_webhooks'`); n != 0 {
		t.Fatal("plugin schema still there")
	}
	if n := r.count(t, `SELECT count(*) FROM pg_roles WHERE rolname LIKE 'hm\_p\_%\_webhooks'`); n != 0 {
		t.Fatal("plugin role still there")
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_plugin_grants WHERE plugin = 'webhooks'`); n != 0 {
		t.Fatal("grants still there")
	}
	if !r.s.webhooksImportRecorded(ctx) {
		t.Fatal("the finished import is not recorded")
	}
	audits := r.count(t, `SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'webhooks.import_plugin' AND detail->>'copied' = '1' AND detail->>'uninstalled' = 'true'`)
	if audits != 1 {
		t.Fatalf("audit rows = %d, want 1", audits)
	}
	var detail string
	_ = r.s.db.Pool().QueryRow(ctx, `SELECT string_agg(detail::text, ' ') FROM hatamoto_admin_audit`).Scan(&detail)
	if strings.Contains(detail, "whsec_") {
		t.Fatal("a secret reached the audit log")
	}

	// Second boot: nothing to find, nothing happens (no second audit row).
	r.s.retireWebhooksPlugin(ctx)
	if n := r.count(t, `SELECT count(*) FROM hatamoto_webhooks`); n != 1 {
		t.Fatalf("subscriptions after a second run = %d", n)
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'webhooks.import_plugin'`); n != 1 {
		t.Fatalf("a second run audited again: %d", n)
	}
}

type failingSource struct{}

func (failingSource) Query(context.Context, string, string, ...any) (*pluginhost.QueryResult, error) {
	return nil, errors.New("role unusable")
}

func TestWebhooksLegacyCopyFailureKeepsThePlugin(t *testing.T) {
	r := newLegacyRig(t)
	ctx := context.Background()

	if _, err := r.s.webhooks.Repo().ImportPluginSubscriptions(ctx, failingSource{}, false); err == nil {
		t.Fatal("a failing source must fail the import")
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_webhooks`); n != 0 {
		t.Fatalf("rows written despite the failure: %d", n)
	}

	// Through the boot step, with the copy failing at the insert.
	// r.s.pluginDB is already a fresh PluginDB, as at boot.
	if _, err := r.s.db.Pool().Exec(ctx, `ALTER TABLE hatamoto_webhooks RENAME TO hatamoto_webhooks_gone`); err != nil {
		t.Fatal(err)
	}
	r.s.retireWebhooksPlugin(ctx) // the insert fails: the target table is missing
	if r.s.webhooksImportRecorded(ctx) {
		t.Fatal("a failed copy counts as an import: the admin would offer Remove")
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_packages WHERE name = 'webhooks'`); n != 1 {
		t.Fatal("the plugin was uninstalled although the copy failed")
	}
	if n := r.count(t, `SELECT count(*) FROM pg_namespace WHERE nspname = 'plugin_webhooks'`); n != 1 {
		t.Fatal("the plugin's data was dropped although the copy failed")
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'webhooks.import_plugin'`); n != 0 {
		t.Fatal("a failed import was audited as done")
	}
}

func (r *legacyRig) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := r.s.db.Pool().Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// A subscription that was not sending as a plugin must not start sending as a core one.
func TestWebhooksLegacyImportIsPausedUnlessThePluginWasSending(t *testing.T) {
	const id = "11111111-1111-1111-1111-111111111111"
	cases := []struct {
		name       string
		prepare    string
		wantActive bool
	}{
		{"enabled and granted keeps the flags", ``, true},
		{"disabled plugin", `UPDATE hatamoto_packages SET enabled = false WHERE name = 'webhooks'`, false},
		{"uninstalling plugin", `UPDATE hatamoto_packages SET state = 'uninstalling' WHERE name = 'webhooks'`, false},
		{"no http.fetch.any grant", `UPDATE hatamoto_plugin_grants SET permissions = '{kv,db,http.fetch}' WHERE plugin = 'webhooks'`, false},
		{"no http.fetch grant", `UPDATE hatamoto_plugin_grants SET permissions = '{kv,db,http.fetch.any}' WHERE plugin = 'webhooks'`, false},
		{"no db grant", `UPDATE hatamoto_plugin_grants SET permissions = '{kv,http.fetch,http.fetch.any}' WHERE plugin = 'webhooks'`, false},
		{"signer no longer trusted", ``, false},
		{"no grants at all", `DELETE FROM hatamoto_plugin_grants WHERE plugin = 'webhooks'`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLegacyRig(t)
			if c.prepare != "" {
				r.exec(t, c.prepare)
			}
			if c.name == "signer no longer trusted" { // HATAMOTO_TRUST_FIRST_PARTY=false
				r.s.packages.Trust = packages.TrustSet{}
			}
			r.s.retireWebhooksPlugin(context.Background())
			got, err := r.s.webhooks.Get(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if got.Active != c.wantActive {
				t.Fatalf("active = %v, want %v", got.Active, c.wantActive)
			}
			if n := r.count(t, `SELECT count(*) FROM hatamoto_packages WHERE name = 'webhooks'`); n != 0 {
				t.Fatal("the plugin must be uninstalled either way")
			}
		})
	}
}

// Anything but the first-party plugin is left alone: no import, no uninstall, no drop.
func TestWebhooksLegacyOnlyRetiresTheFirstPartyPlugin(t *testing.T) {
	cases := []struct {
		name    string
		prepare string
	}{
		{"third-party signer", `UPDATE hatamoto_packages SET signer_key_id = 'deadbeefdeadbeef' WHERE name = 'webhooks'`},
		{"operator-signed", `UPDATE hatamoto_packages SET signer_key_id = 'f00dfeedf00dfeed' WHERE name = 'webhooks'`},
		{"unsigned", `UPDATE hatamoto_packages SET signer_key_id = NULL, signature = NULL WHERE name = 'webhooks'`},
		{"schema without a package row", `DELETE FROM hatamoto_packages WHERE name = 'webhooks'`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLegacyRig(t)
			r.exec(t, c.prepare)
			r.s.retireWebhooksPlugin(context.Background())
			if n := r.count(t, `SELECT count(*) FROM hatamoto_webhooks`); n != 0 {
				t.Fatalf("imported %d subscription(s) from a plugin that is not first-party", n)
			}
			if n := r.count(t, `SELECT count(*) FROM pg_namespace WHERE nspname = 'plugin_webhooks'`); n != 1 {
				t.Fatal("the plugin's data was dropped")
			}
			want := 1
			if c.name == "schema without a package row" {
				want = 0
			}
			if n := r.count(t, `SELECT count(*) FROM hatamoto_packages WHERE name = 'webhooks'`); n != want {
				t.Fatalf("package rows = %d, want %d", n, want)
			}
			if n := r.count(t, `SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'webhooks.import_plugin'`); n != 0 {
				t.Fatal("a skipped retire was audited as done")
			}
		})
	}
}

// The retired first-party key signed the plugin too: it is moved like any first-party
// one. Its key is no longer trusted here, so the rows come in paused.
func TestWebhooksLegacyRetiredFirstPartyKeyIsRetired(t *testing.T) {
	r := newLegacyRig(t)
	r.exec(t, `UPDATE hatamoto_packages SET signer_key_id = 'a592a1276b3947b1' WHERE name = 'webhooks'`)
	r.s.retireWebhooksPlugin(context.Background())
	got, err := r.s.webhooks.Get(context.Background(), "11111111-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if got.Active {
		t.Error("the retired key is not trusted, the rows must come in paused")
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_packages WHERE name = 'webhooks'`); n != 0 {
		t.Fatal("the plugin must be uninstalled")
	}
}

// A plugin left behind (not first-party) is reported for the admin notice.
func TestWebhooksLegacyPluginLeftBehindIsReported(t *testing.T) {
	r := newLegacyRig(t)
	ctx := context.Background()
	r.exec(t, `UPDATE hatamoto_packages SET signer_key_id = 'deadbeefdeadbeef' WHERE name = 'webhooks'`)
	r.s.retireWebhooksPlugin(ctx)
	if !r.s.webhooksPluginLeftBehind(ctx) {
		t.Error("a left-behind webhooks plugin is not reported")
	}
	r.exec(t, `DELETE FROM hatamoto_packages WHERE name = 'webhooks'`)
	if r.s.webhooksPluginLeftBehind(ctx) {
		t.Error("reported with no plugin installed")
	}
}

// A first-party plugin row is not "left behind": the boot step retires it, so
// no notice is shown. Third-party and unsigned rows are.
func TestWebhooksLegacyPluginLeftBehindIgnoresFirstParty(t *testing.T) {
	cases := []struct {
		name    string
		prepare string
		want    bool
	}{
		{"first-party", `UPDATE hatamoto_packages SET signer_key_id = 'adea8e00f5ac74ca' WHERE name = 'webhooks'`, false},
		{"retired first-party key", `UPDATE hatamoto_packages SET signer_key_id = 'a592a1276b3947b1' WHERE name = 'webhooks'`, false},
		{"third-party", `UPDATE hatamoto_packages SET signer_key_id = 'deadbeefdeadbeef' WHERE name = 'webhooks'`, true},
		{"unsigned", `UPDATE hatamoto_packages SET signer_key_id = NULL, signature = NULL WHERE name = 'webhooks'`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLegacyRig(t)
			r.exec(t, c.prepare)
			if got := r.s.webhooksPluginLeftBehind(context.Background()); got != c.want {
				t.Errorf("left behind = %v, want %v", got, c.want)
			}
		})
	}
}
