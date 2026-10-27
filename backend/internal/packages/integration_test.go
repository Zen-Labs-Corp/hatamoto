//go:build integration

package packages

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/migrations"
	"hatamoto.dev/sdk"
)

// scratchDB makes a migrated database owned by a plain (non-superuser) role
// that may create roles, like production, so plugin provisioning is real.
func scratchDB(t *testing.T) *pgxpool.Pool {
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
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	var super bool
	_ = admin.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super)
	owner := ""
	if super {
		var exists bool
		_ = admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'hm_pk_app')`).Scan(&exists)
		if !exists {
			if _, err := admin.Exec(ctx, `CREATE ROLE hm_pk_app LOGIN PASSWORD 'x' CREATEROLE`); err != nil {
				t.Fatal(err)
			}
		}
		owner = " OWNER hm_pk_app"
		cfg.ConnConfig.User, cfg.ConnConfig.Password = "hm_pk_app", "x"
	}
	name := "hm_pk_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name+owner); err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		rows, _ := admin.Query(ctx, `SELECT rolname FROM pg_roles WHERE rolname LIKE 'hm\_p\_%'`)
		var roles []string
		for rows != nil && rows.Next() {
			var r string
			_ = rows.Scan(&r)
			roles = append(roles, r)
		}
		if rows != nil {
			rows.Close()
		}
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		for _, r := range roles {
			_, _ = admin.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{r}.Sanitize())
		}
		admin.Close(ctx)
	})
	prov, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prov.Up(ctx); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	return pool
}

// coreChecksum is a row count and content hash of every core table: all tables
// except the ones packages and plugins own.
func coreChecksum(t *testing.T, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public'
		AND tablename NOT LIKE 'hatamoto\_package%' AND tablename NOT IN ('hatamoto_theme_settings', 'hatamoto_admin_audit')
		AND tablename NOT LIKE 'hatamoto\_plugin\_%' AND tablename NOT LIKE 'goose%' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		tables = append(tables, n)
	}
	rows.Close()
	out := map[string]string{}
	for _, tb := range tables {
		var n int
		var sum string
		q := `SELECT count(*), md5(COALESCE(string_agg(x::text, '|' ORDER BY x::text), '')) FROM ` + pgx.Identifier{tb}.Sanitize() + ` x`
		if err := pool.QueryRow(ctx, q).Scan(&n, &sum); err != nil {
			t.Fatalf("checksum %s: %v", tb, err)
		}
		out[tb] = sum + "/" + string(rune('0'+n%10)) + itoa(n)
	}
	if len(out) < 20 {
		t.Fatalf("only %d core tables checked", len(out))
	}
	return out
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

type rig struct {
	t      *testing.T
	pool   *pgxpool.Pool
	mgr    *Manager
	rt     *pluginhost.Runtime
	eng    *render.Engine
	key    Key
	reg    *httptest.Server
	served map[string][]byte // path -> body
	idx    Index
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool := scratchDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rt, err := pluginhost.NewRuntime(log, hooks.New(log), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(context.Background()) })
	pdb := pluginhost.NewPluginDB(pool, log)
	grants := pluginhost.NewGrantStore(pool)
	rt.Configure(pluginhost.Config{Grants: grants, Services: pluginhost.Services{DB: pdb, Settings: pluginhost.NewSettingsStore(pool)}})
	eng, err := render.New(render.Options{Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, pool: pool, rt: rt, eng: eng, served: map[string][]byte{}}
	r.key = newKey(t)
	r.reg = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/registry.json" {
			doc, err := SignIndex(mustJSON(r.idx), r.key)
			if err != nil {
				t.Error(err)
			}
			_, _ = w.Write(doc)
			return
		}
		b, ok := r.served[req.URL.Path]
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(r.reg.Close)
	r.mgr = NewManager(Options{
		Pool: pool, Trust: trust(t, r.key), Plugins: rt, Grants: grants, Dropper: pdb, Engine: eng, AllowDev: true, Logger: log,
		Registry: &Registry{URL: r.reg.URL + "/registry.json", Client: r.reg.Client(), Trust: trust(t, r.key), AllowInsecure: true},
		NewPreview: func(layers []fs.FS, settings map[string]any) (*render.Engine, error) {
			return render.New(render.Options{Layers: layers, Settings: settings, Logger: log})
		},
	})
	return r
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// publish packs files, signs them with key (nil: unsigned) and lists the
// package in the registry.
func (r *rig) publish(kind Kind, files map[string][]byte, key *Key) Entry {
	r.t.Helper()
	raw, err := PackFiles(files, key)
	if err != nil {
		r.t.Fatal(err)
	}
	p, err := Parse(raw, DefaultLimits)
	if err != nil {
		r.t.Fatal(err)
	}
	sum := p.SHA256()
	path := "/" + p.Name + "-" + p.Version + Extension
	r.served[path] = raw
	e := Entry{Kind: kind, Name: p.Name, Version: p.Version, URL: path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(raw))}
	kept := []Entry{}
	for _, x := range r.idx.Packages {
		if x.Name != e.Name {
			kept = append(kept, x)
		}
	}
	now := time.Now().UTC()
	r.idx = Index{Version: 1, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(30 * 24 * time.Hour).Format(time.RFC3339), Packages: append(kept, e)}
	return e
}

func (r *rig) plugin(version string) map[string][]byte {
	m := sdk.Manifest{Name: "capa", Version: version, SDK: "^0.7",
		Permissions: []string{sdk.PermKV, sdk.PermDB, sdk.PermLog},
		Routes:      []sdk.Route{{Method: "GET", Path: "/echo"}},
		UI:          &sdk.AdminUI{Title: "Capa"},
	}
	return map[string][]byte{
		PluginManifest:            mustJSON(m),
		PluginWasm:                plugintest.Build(r.t, "capa"),
		PluginAssetCSS:            []byte("body{outline:1px solid red}"),
		"migrations/001_init.sql": []byte("-- +goose Up\nCREATE TABLE notes (id serial primary key, body text);\nINSERT INTO notes (body) VALUES ('hello');\n"),
		"ui/index.html":           []byte("<h1>capa</h1>"),
	}
}

func (r *rig) theme(version string) map[string][]byte {
	return map[string][]byte{
		ThemeManifest:          []byte(`{"name":"sunrise","version":"` + version + `","settings_schema":{"type":"object","properties":{"accent":{"type":"string","default":"teal"}}}}`),
		"partials/footer.html": []byte(`{{define "footer"}}<footer id="sunrise">accent={{.Settings.accent}}</footer>{{end}}`),
		"sections/promo.html":  []byte(`{{define "section-promo"}}<aside>promo</aside>{{end}}`),
		"schemas/promo.json":   []byte(`{"x-hm-section":{"title":"Promo"},"type":"object","properties":{}}`),
	}
}

func (r *rig) page() string {
	r.t.Helper()
	var sb strings.Builder
	v := &render.PageView{BaseURL: "https://example.test", Path: "/", Site: render.SiteView{Name: "T", Language: "en"},
		Page: render.PageMeta{Name: "T", Title: "T"}}
	if err := r.eng.RenderPage(&sb, v); err != nil {
		r.t.Fatal(err)
	}
	return sb.String()
}

func TestPluginAndThemeInstallFromSignedRegistryWithoutRestart(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	before := coreChecksum(t, r.pool)
	// Plugin roles are cluster-wide and named after this database's id.
	var roleLike string
	if err := r.pool.QueryRow(ctx, `SELECT 'hm\_p\_' || left(id, 8) || '\_%' FROM hatamoto_instance`).Scan(&roleLike); err != nil {
		t.Fatal(err)
	}

	// --- plugin: install with a permission prompt result, live at once ---
	r.publish(KindPlugin, r.plugin("1.0.0"), &r.key)
	idx, err := r.mgr.Registry.Fetch(ctx)
	if err != nil || len(idx.Packages) != 1 {
		t.Fatalf("registry: %v %+v", err, idx)
	}
	got, err := r.mgr.InstallFromRegistry(ctx, KindPlugin, "capa", InstallRequest{Grant: []string{sdk.PermKV, sdk.PermDB}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == nil || !got.Status.Loaded || got.Status.DataError != "" {
		t.Fatalf("plugin status: %+v", got.Status)
	}
	var schemaExists bool
	_ = r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, pluginhost.SchemaName("capa")).Scan(&schemaExists)
	if !schemaExists {
		t.Fatal("the plugin's schema was not provisioned")
	}
	if !strings.Contains(r.page(), `href="/p/capa/assets/plugin.css"`) {
		t.Fatal("the plugin's public stylesheet is not linked from pages")
	}
	if r.mgr.PluginPackage("capa") == nil || !r.mgr.PluginPackage("capa").Has("ui/index.html") {
		t.Fatal("plugin package files are not reachable")
	}

	// --- theme: install, preview, activate, settings ---
	r.publish(KindTheme, r.theme("1.0.0"), &r.key)
	if _, err := r.mgr.InstallFromRegistry(ctx, KindTheme, "sunrise", InstallRequest{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.page(), `id="sunrise"`) {
		t.Fatal("installing a theme must not activate it")
	}
	pe, err := r.mgr.PreviewEngine(ctx, "sunrise")
	if err != nil {
		t.Fatal(err)
	}
	var pv strings.Builder
	_ = pe.RenderPage(&pv, &render.PageView{BaseURL: "https://example.test", Path: "/", Site: render.SiteView{Name: "T", Language: "en"}, Page: render.PageMeta{Name: "T", Title: "T"}})
	if !strings.Contains(pv.String(), `accent=teal`) || strings.Contains(r.page(), `id="sunrise"`) {
		t.Fatalf("preview must show the theme without activating it:\n%s", pv.String())
	}
	if err := r.mgr.Activate(ctx, "sunrise", false); err != nil {
		t.Fatal(err)
	}
	if out := r.page(); !strings.Contains(out, `<footer id="sunrise">accent=teal</footer>`) {
		t.Fatalf("active theme not applied:\n%s", out)
	}
	if errs, err := r.mgr.SaveThemeSettings(ctx, "sunrise", map[string]any{"accent": 7}); err != nil || len(errs) == 0 {
		t.Fatalf("a bad settings value must be a field error: %v %v", errs, err)
	}
	if errs, err := r.mgr.SaveThemeSettings(ctx, "sunrise", map[string]any{"accent": "rose"}); err != nil || len(errs) != 0 {
		t.Fatalf("save: %v %v", errs, err)
	}
	if out := r.page(); !strings.Contains(out, "accent=rose") {
		t.Fatalf("settings not live:\n%s", out)
	}
	if err := r.mgr.Uninstall(ctx, KindTheme, "sunrise", false); !errors.Is(err, ErrThemeActive) {
		t.Fatalf("an active theme cannot be uninstalled: %v", err)
	}
	if err := r.mgr.Activate(ctx, "", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.page(), `id="sunrise"`) {
		t.Fatal("default theme not restored")
	}
	if err := r.mgr.Uninstall(ctx, KindTheme, "sunrise", false); err != nil {
		t.Fatal(err)
	}

	// --- plugin: disable, enable, update, uninstall with data drop ---
	if err := r.mgr.SetEnabled(ctx, "capa", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.page(), "/p/capa/") || r.mgr.PluginPackage("capa") != nil {
		t.Fatal("a disabled plugin must not serve")
	}
	if err := r.mgr.SetEnabled(ctx, "capa", true); err != nil {
		t.Fatal(err)
	}
	r.publish(KindPlugin, r.plugin("1.1.0"), &r.key)
	if got, err := r.mgr.InstallFromRegistry(ctx, KindPlugin, "capa", InstallRequest{}); err != nil || !got.Updated || got.Status.Manifest.Version != "1.1.0" || !got.Status.Loaded {
		t.Fatalf("update: %+v %v", got, err)
	}
	if g := r.rt.Grants().Granted("capa"); len(g) != 2 {
		t.Fatalf("an update keeps the owner's grants: %v", g)
	}
	if err := r.mgr.Uninstall(ctx, KindPlugin, "capa", true); err != nil {
		t.Fatal(err)
	}
	_ = r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, pluginhost.SchemaName("capa")).Scan(&schemaExists)
	var roles int
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM pg_roles WHERE rolname LIKE $1`, roleLike).Scan(&roles)
	if schemaExists || roles != 0 || len(r.rt.Statuses()) != 0 {
		t.Fatalf("data drop left schema=%v roles=%d statuses=%d", schemaExists, roles, len(r.rt.Statuses()))
	}
	var rowsLeft int
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_packages`).Scan(&rowsLeft)
	if rowsLeft != 0 || strings.Contains(r.page(), "/p/capa/") {
		t.Fatal("uninstall left package rows or asset links")
	}

	// --- uninstall changed no core data ---
	after := coreChecksum(t, r.pool)
	for tb, sum := range before {
		if after[tb] != sum {
			t.Errorf("core table %s changed: %s -> %s", tb, sum, after[tb])
		}
	}
	if len(after) != len(before) {
		t.Errorf("core table set changed: %d -> %d", len(before), len(after))
	}
}

func TestHostilePackagesAreNotInstalled(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	other := newKey(t)

	count := func() int {
		var n int
		_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_packages`).Scan(&n)
		return n
	}
	pack := func(files map[string][]byte, key *Key) []byte {
		b, err := PackFiles(files, key)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	t.Run("unsigned is refused until the owner opts in", func(t *testing.T) {
		raw := pack(r.theme("1.0.0"), nil)
		if _, err := r.mgr.Install(ctx, raw, InstallRequest{Source: SourceUpload}); !errors.Is(err, ErrUnsignedNotAllowed) {
			t.Fatalf("err = %v", err)
		}
		if count() != 0 {
			t.Fatal("a refused package was stored")
		}
		if err := r.mgr.SetAllowUnsigned(ctx, true); err != nil {
			t.Fatal(err)
		}
		if _, err := r.mgr.Install(ctx, raw, InstallRequest{Source: SourceUpload}); err != nil {
			t.Fatalf("opted in: %v", err)
		}
		_ = r.mgr.Uninstall(ctx, KindTheme, "sunrise", false)
		_ = r.mgr.SetAllowUnsigned(ctx, false)
	})
	t.Run("wrong signer", func(t *testing.T) {
		if _, err := r.mgr.Install(ctx, pack(r.theme("1.0.0"), &other), InstallRequest{Source: SourceUpload}); !errors.Is(err, ErrUntrustedKey) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("bad signature", func(t *testing.T) {
		files := r.theme("1.0.0")
		signed, _ := Parse(pack(files, &r.key), DefaultLimits)
		files["partials/footer.html"] = []byte(`{{define "footer"}}tampered{{end}}`)
		files[SignatureFile] = mustJSON(signed.Signature)
		raw, err := zipFiles(files)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.mgr.Install(ctx, raw, InstallRequest{Source: SourceUpload}); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("zip slip and bomb", func(t *testing.T) {
		slip := rawZip(t, hdr(ThemeManifest, themeJSON), hdr("assets/../../../etc/cron.d/x", "x"))
		if _, err := r.mgr.Install(ctx, slip, InstallRequest{Source: SourceUpload}); err == nil {
			t.Fatal("zip slip installed")
		}
		bomb := rawZip(t, hdr(ThemeManifest, themeJSON), hdr("assets/b.bin", strings.Repeat("\x00", 8<<20)))
		if _, err := r.mgr.Install(ctx, bomb, InstallRequest{Source: SourceUpload}); err == nil {
			t.Fatal("zip bomb installed")
		}
	})
	t.Run("registry index signed by an untrusted key", func(t *testing.T) {
		r.publish(KindTheme, r.theme("1.0.0"), &r.key)
		saved := r.key
		r.key = other // the server now signs the index with a key we do not trust
		_, err := r.mgr.Registry.Fetch(ctx)
		r.key = saved
		if !errors.Is(err, ErrUntrustedKey) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("registry file does not match its listed hash", func(t *testing.T) {
		e := r.publish(KindTheme, r.theme("1.0.0"), &r.key)
		r.served[e.URL] = pack(r.theme("9.9.9"), &r.key) // swapped after listing
		if _, err := r.mgr.InstallFromRegistry(ctx, KindTheme, "sunrise", InstallRequest{}); err == nil || !strings.Contains(err.Error(), "hash") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a theme that does not render", func(t *testing.T) {
		files := r.theme("1.0.0")
		files["partials/footer.html"] = []byte(`{{define "footer"}}{{ .Nope.Deeper }}{{end}}`)
		if _, err := r.mgr.Install(ctx, pack(files, &r.key), InstallRequest{Source: SourceUpload}); err == nil {
			t.Fatal("a broken theme was installed")
		}
	})
	t.Run("dev packages need dev mode", func(t *testing.T) {
		r.mgr.AllowDev = false
		_, err := r.mgr.Install(ctx, pack(r.theme("1.0.0"), nil), InstallRequest{Source: SourceDev})
		r.mgr.AllowDev = true
		if !errors.Is(err, ErrDevDisabled) {
			t.Fatalf("err = %v", err)
		}
	})
	if count() != 0 {
		t.Fatalf("%d hostile packages were stored", count())
	}
}

func TestMissingSectionsBlockActivation(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	var schemaID uuid.UUID
	if err := r.pool.QueryRow(ctx, `SELECT id FROM hatamoto_entity_schemas LIMIT 1`).Scan(&schemaID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes)
		VALUES ($1, 'p', 'p', 'published', '{"page_sections":[{"type":"hero"},{"type":"galaxy"},{"type":"galaxy"}]}')`, schemaID); err != nil {
		t.Fatal(err)
	}
	r.publish(KindTheme, r.theme("1.0.0"), &r.key)
	if _, err := r.mgr.InstallFromRegistry(ctx, KindTheme, "sunrise", InstallRequest{}); err != nil {
		t.Fatal(err)
	}
	var miss *MissingSectionsError
	err := r.mgr.Activate(ctx, "sunrise", false)
	if !errors.As(err, &miss) || len(miss.Missing) != 1 || miss.Missing[0].Type != "galaxy" || miss.Missing[0].Uses != 2 {
		t.Fatalf("err = %v, want galaxy x2 missing", err)
	}
	if r.mgr.ActiveTheme() != DefaultTheme {
		t.Fatal("a blocked activation changed the theme")
	}
	if err := r.mgr.Activate(ctx, "sunrise", true); err != nil || r.mgr.ActiveTheme() != "sunrise" {
		t.Fatalf("forced: %v active=%s", err, r.mgr.ActiveTheme())
	}
}

// A section type used only inside a page_doc component is reported; the frozen
// page_sections mirror of a doc page is not read.
func TestMissingSectionsReadsPageDoc(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	var schemaID uuid.UUID
	if err := r.pool.QueryRow(ctx, `SELECT id FROM hatamoto_entity_schemas LIMIT 1`).Scan(&schemaID); err != nil {
		t.Fatal(err)
	}
	doc := `{"v":2,"nodes":[{"id":"s","t":"container","p":{"tag":"section","v1":true},"c":[{"id":"k","t":"component","p":{"type":"nebula","content":{}}}]}]}`
	if _, err := r.pool.Exec(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes)
		VALUES ($1, 'd', 'd', 'published', jsonb_build_object('page_doc', $2::jsonb, 'page_sections', '[{"type":"ghost"}]'::jsonb))`, schemaID, doc); err != nil {
		t.Fatal(err)
	}
	miss, err := r.mgr.MissingSections(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(miss) != 1 || miss[0].Type != "nebula" || miss[0].Uses != 1 {
		t.Fatalf("missing = %+v, want nebula x1 only", miss)
	}
}

func TestReconcilePicksUpAnotherProcessesInstall(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	// The CLI has no runtime or engine: it only writes the database.
	cli := NewManager(Options{Pool: r.pool, Trust: trust(t, r.key), AllowDev: true})
	raw, _ := PackFiles(r.theme("1.0.0"), &r.key)
	if _, err := cli.Install(ctx, raw, InstallRequest{Source: SourceUpload}); err != nil {
		t.Fatal(err)
	}
	if err := cli.store.SetActiveTheme(ctx, "sunrise"); err != nil {
		t.Fatal(err)
	}
	r.mgr.Reconcile(ctx)
	if !strings.Contains(r.page(), `id="sunrise"`) {
		t.Fatalf("the server did not pick up the CLI's install:\n%s", r.page())
	}
}

// A plugin installed by the CLI with --grant is loaded by the running server
// with that grant: the server reads the grants from the database when it loads
// a package another process installed.
func TestReconcileReloadsGrantsOfAnotherProcessesInstall(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	cli := NewManager(Options{Pool: r.pool, Trust: trust(t, r.key), AllowDev: true})
	raw, err := PackFiles(r.plugin("1.0.0"), &r.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Install(ctx, raw, InstallRequest{Source: SourceUpload, Grant: []string{sdk.PermKV}}); err != nil {
		t.Fatal(err)
	}
	if r.mgr.Grants.Has("capa", sdk.PermKV) {
		t.Fatal("the server knew the grant before it reconciled")
	}
	r.mgr.Reconcile(ctx)
	if !r.mgr.Grants.Has("capa", sdk.PermKV) {
		t.Fatal("the server did not read the CLI's grant")
	}
}

// A plugin's browser JavaScript is linked on public pages only while the owner
// grants site.scripts. Covers a fresh install that asks for it, and one that
// shipped plugin.js before the permission existed (its manifest does not ask).
// Pages never name the script of a plugin the owner has not granted, however
// hostile that script is; plugin.css stays ungated.
// hostileTemplates are the section and partial sources a plugin with no
// permission might ship to get script onto a page without plugin.js.
var hostileTemplates = map[string]string{
	"inline script":     `<script>fetch("/admin/users",{method:"POST"})</script>`,
	"external script":   `<script src="/p/capa/assets/x.js"></script>`,
	"onerror handler":   `<img src=x onerror="alert(1)">`,
	"javascript: href":  `<a href="javascript:alert(1)">go</a>`,
	"split script open": `<scr{{if .Content}}{{end}}ipt>alert(1)</script>`,
	// An action where an element or attribute name is parsed: the source scan
	// of one branch and the sample render on empty data do not see these.
	"name from constant, behind if": `{{if .Content.x}}<scr{{"ipt"}}>x()</scr{{"ipt"}}>{{end}}`,
	"name from data":                `<scr{{.Content.t}}>x()</scr{{.Content.t}}>`,
	"name behind range":             `{{range .Content.items}}<scr{{"ipt"}}>x()</scr{{"ipt"}}>{{end}}`,
	"attribute name behind if":      `{{if .Content.x}}<p {{"o"}}{{"nclick"}}="x()">x</p>{{end}}`,
}

// pageWithSections renders the page with one block of each type, as a
// render.page result would add them.
func (r *rig) pageWithSections(types ...string) string {
	r.t.Helper()
	var sb strings.Builder
	v := &render.PageView{BaseURL: "https://example.test", Path: "/", Site: render.SiteView{Name: "T", Language: "en"},
		Page: render.PageMeta{Name: "T", Title: "T"}}
	for _, typ := range types {
		v.Sections = append(v.Sections, render.Section{ID: "b-" + typ, Type: typ, Content: map[string]any{}})
	}
	if err := r.eng.RenderPage(&sb, v); err != nil {
		r.t.Fatal(err)
	}
	return sb.String()
}

func (r *rig) installPlugin(files map[string][]byte, perms []string, req InstallRequest) error {
	r.t.Helper()
	var man sdk.Manifest
	_ = json.Unmarshal(files[PluginManifest], &man)
	man.Permissions = perms
	files[PluginManifest] = mustJSON(man)
	raw, err := PackFiles(files, &r.key)
	if err != nil {
		r.t.Fatal(err)
	}
	req.Source = SourceUpload
	_, err = r.mgr.Install(context.Background(), raw, req)
	r.mgr.Reconcile(context.Background())
	return err
}

func TestHostilePluginJS_NotLinkedWithoutGrant(t *testing.T) {
	const js = "/p/capa/assets/plugin.js"
	hostileJS := []byte(`fetch("/admin/users",{method:"POST",credentials:"include",body:"role=owner"})`)
	for name, existing := range map[string]bool{
		"fresh install that asks for site.scripts":  false,
		"existing install from before site.scripts": true,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			ctx := context.Background()
			t.Cleanup(func() { render.SetPluginScriptGate(nil); render.RemovePlugin("capa") })
			files := r.plugin("1.0.0")
			files[PluginAssetJS] = hostileJS
			if existing {
				// 1.0.0 predates site.scripts: it asks for kv only and nothing is granted.
				if err := r.installPlugin(files, []string{sdk.PermKV}, InstallRequest{}); err != nil {
					t.Fatal(err)
				}
				if got := r.rt.Grants().Granted("capa"); slices.Contains(got, sdk.PermSiteScripts) {
					t.Fatalf("granted before the host knew site.scripts: %v", got)
				}
				if strings.Contains(r.page(), js) {
					t.Fatal("plugin.js linked before the upgrade")
				}
				// 1.1.0 is the upgrade that newly asks for it: the owner must confirm,
				// and the grant is not carried over.
				files = r.plugin("1.1.0")
				files[PluginAssetJS] = hostileJS
				if err := r.installPlugin(files, []string{sdk.PermKV, sdk.PermSiteScripts}, InstallRequest{}); err == nil {
					t.Fatal("an update that newly asks for site.scripts installed without confirmation")
				}
				if err := r.installPlugin(files, []string{sdk.PermKV, sdk.PermSiteScripts}, InstallRequest{Confirm: true}); err != nil {
					t.Fatal(err)
				}
			} else if err := r.installPlugin(files, []string{sdk.PermKV, sdk.PermSiteScripts}, InstallRequest{}); err != nil {
				t.Fatal(err)
			}
			page := r.page()
			if strings.Contains(page, js) {
				t.Fatalf("plugin.js linked without a grant:\n%s", page)
			}
			if !strings.Contains(page, "/p/capa/assets/plugin.css") {
				t.Errorf("plugin.css must not need a grant:\n%s", page)
			}
			if !r.mgr.ScriptsBlocked("capa") {
				t.Error("the admin banner would not show: ScriptsBlocked is false")
			}
			if _, err := r.rt.Grant(ctx, "capa", []sdk.Permission{sdk.PermSiteScripts}); err != nil {
				t.Fatalf("grant site.scripts: %v", err)
			}
			if page := r.page(); !strings.Contains(page, js) {
				t.Fatal("after granting the script is not linked")
			}
			if r.mgr.ScriptsBlocked("capa") {
				t.Error("still flagged blocked after the grant")
			}
			if _, err := r.rt.Grant(ctx, "capa", nil); err != nil {
				t.Fatal(err)
			}
			if page := r.page(); strings.Contains(page, js) {
				t.Fatal("after the revoke the script is still linked")
			}
		})
	}
}

// A plugin's sections and partials are held to the same script check as
// plugin.js: blocked without site.scripts, present with it, blocked again after
// the revoke, with no reinstall in between.
func TestHostilePluginJS_SectionsAndPartialsNeedTheGrant(t *testing.T) {
	for name, markup := range hostileTemplates {
		for _, kind := range []string{"section", "partial"} {
			t.Run(kind+"/"+name, func(t *testing.T) {
				r := newRig(t)
				ctx := context.Background()
				t.Cleanup(func() { render.SetPluginScriptGate(nil); render.RemovePlugin("capa") })
				files := r.plugin("1.0.0")
				files["sections/plain.html"] = []byte(`{{define "section-plain"}}<p id="plain-ok">ok</p>{{end}}`)
				switch kind {
				case "section":
					files["sections/evil.html"] = []byte(`{{define "section-evil"}}<b id="evil-box">` + markup + `</b>{{end}}`)
				default:
					files["partials/card.html"] = []byte(`<b id="evil-box">` + markup + `</b>`)
					files["sections/evil.html"] = []byte(`{{define "section-evil"}}{{template "plugin/capa/card" .}}{{end}}`)
				}
				if err := r.installPlugin(files, []string{sdk.PermKV, sdk.PermSiteScripts}, InstallRequest{}); err != nil {
					t.Fatal(err)
				}
				present := func() bool { return strings.Contains(r.pageWithSections("evil", "plain"), `id="evil-box"`) }
				if present() {
					t.Fatal("hostile markup on the page without site.scripts")
				}
				if !strings.Contains(r.pageWithSections("evil", "plain"), `id="plain-ok"`) {
					t.Error("a harmless section of the same plugin must keep working")
				}
				if len(r.mgr.MarkupStopped("capa")) == 0 {
					t.Error("the admin would not say why the markup is stopped")
				}
				if _, err := r.rt.Grant(ctx, "capa", []sdk.Permission{sdk.PermSiteScripts}); err != nil {
					t.Fatal(err)
				}
				if !present() {
					t.Fatal("with site.scripts the section must behave as before")
				}
				if len(r.mgr.MarkupStopped("capa")) != 0 {
					t.Error("still reported as stopped after the grant")
				}
				if _, err := r.rt.Grant(ctx, "capa", nil); err != nil {
					t.Fatal(err)
				}
				if present() {
					t.Fatal("after the revoke the hostile markup is on the page again")
				}
			})
		}
	}
}

// A grant row outlives an update that stops asking for site.scripts, but it no
// longer counts: the script is not linked and the sections are checked.
func TestStaleSiteScriptsGrantDoesNotCount(t *testing.T) {
	const js = "/p/capa/assets/plugin.js"
	r := newRig(t)
	ctx := context.Background()
	t.Cleanup(func() { render.SetPluginScriptGate(nil); render.RemovePlugin("capa") })
	files := r.plugin("1.0.0")
	files[PluginAssetJS] = []byte(`fetch("/admin/users")`)
	files["sections/evil.html"] = []byte(`{{define "section-evil"}}<b id="evil-box"><script>alert(1)</script></b>{{end}}`)
	if err := r.installPlugin(files, []string{sdk.PermKV, sdk.PermSiteScripts}, InstallRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.rt.Grant(ctx, "capa", []sdk.Permission{sdk.PermSiteScripts}); err != nil {
		t.Fatal(err)
	}
	if page := r.pageWithSections("evil"); !strings.Contains(page, js) || !strings.Contains(page, `id="evil-box"`) {
		t.Fatalf("with the grant and the request both in place the plugin must work:\n%s", page)
	}
	// 1.1.0 drops the permission from its manifest.
	files = r.plugin("1.1.0")
	files[PluginAssetJS] = []byte(`fetch("/admin/users")`)
	files["sections/evil.html"] = []byte(`{{define "section-evil"}}<b id="evil-box"><script>alert(1)</script></b>{{end}}`)
	if err := r.installPlugin(files, []string{sdk.PermKV}, InstallRequest{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(r.rt.Grants().Granted("capa"), sdk.PermSiteScripts) {
		t.Fatal("the grant row did not survive the update, so there is no stale grant to test")
	}
	page := r.pageWithSections("evil")
	if strings.Contains(page, js) {
		t.Errorf("plugin.js linked on a stale grant:\n%s", page)
	}
	if strings.Contains(page, `id="evil-box"`) {
		t.Error("the script section is on the page on a stale grant")
	}
	if len(r.mgr.MarkupStopped("capa")) == 0 {
		t.Error("the admin would not say why the markup is stopped")
	}
}
