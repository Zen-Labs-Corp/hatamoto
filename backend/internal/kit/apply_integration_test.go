//go:build integration

package kit

import (
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/siteconfig"
)

// kitRig is a migrated scratch database with the real services and a package
// manager that reads a signed registry served over httptest.
type kitRig struct {
	t    *testing.T
	d    Deps
	mgr  *packages.Manager
	ents *entity.Service
	mnus *menu.Service
	key  packages.Key
	idx  packages.Index
	blob map[string][]byte
}

func newKitRig(t *testing.T) *kitRig {
	t.Helper()
	pool, _ := scratchDB(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.NewWithConfig(ctx, pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_site_config (company_name, language) VALUES ('Example', 'en') ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}

	r := &kitRig{t: t, blob: map[string][]byte{}}
	if r.key, err = packages.GenerateKey(); err != nil {
		t.Fatal(err)
	}
	pub, err := r.key.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	trust := packages.TrustSet{}
	trust.Add(pub)

	sites := siteconfig.NewStore(db, log)
	r.ents = entity.NewService(entity.NewPostgresSchemaRepository(db), entity.NewPostgresEntityRepository(db), log)
	r.ents.URLRegistry().SetLocales(sites.LocaleSource())
	r.mnus = menu.NewService(menu.NewPostgresRepository(db), log)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/registry.json" {
			b, err := jsonx.Marshal(r.idx)
			if err != nil {
				t.Error(err)
			}
			doc, err := packages.SignIndex(b, r.key)
			if err != nil {
				t.Error(err)
			}
			_, _ = w.Write(doc)
			return
		}
		if b, ok := r.blob[req.URL.Path]; ok {
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, req)
	}))
	t.Cleanup(srv.Close)
	r.mgr = packages.NewManager(packages.Options{
		Pool: pool, Trust: trust, MenuSeeder: r.mnus, Logger: log,
		Registry: &packages.Registry{URL: srv.URL + "/registry.json", Client: srv.Client(), Trust: trust, AllowInsecure: true},
	})
	r.d = Wire(Services{
		Manager: r.mgr, Entities: r.ents, Menus: r.mnus, Site: sites, Comments: comment.New(db, "test-secret", log),
		Pool: pool, Address: func(context.Context) string { return "https://example.test" }, Log: log,
	})
	return r
}

// publish signs files into a package and lists it.
func (r *kitRig) publish(kind packages.Kind, name, version string, files map[string][]byte) {
	r.t.Helper()
	raw, err := packages.PackFiles(files, &r.key)
	if err != nil {
		r.t.Fatal(err)
	}
	p, err := packages.Parse(raw, packages.DefaultLimits)
	if err != nil {
		r.t.Fatal(err)
	}
	sum := p.SHA256()
	path := "/" + string(kind) + "-" + name + ".hmp"
	r.blob[path] = raw
	r.idx.Packages = slices.DeleteFunc(r.idx.Packages, func(e packages.Entry) bool { return e.Kind == kind && e.Name == name })
	r.idx.Packages = append(r.idx.Packages, packages.Entry{Kind: kind, Name: name, Version: version, URL: path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(raw))})
	now := time.Now().UTC()
	r.idx.Version = 1
	r.idx.IssuedAt = now.Add(-time.Minute).Format(time.RFC3339)
	r.idx.ExpiresAt = now.Add(30 * 24 * time.Hour).Format(time.RFC3339)
}

// publishFixture lists the community plugin and theme (the theme declares the
// menu location "rail" when menus is not empty) and the kit itself.
func (r *kitRig) publishFixture(menus string, edit func(*packages.Kit)) {
	r.t.Helper()
	r.publish(packages.KindPlugin, "community-plug", "1.2.0", map[string][]byte{
		"plugin.json": []byte(`{"name":"community-plug","version":"1.2.0","sdk":"^0.27","permissions":["db","settings","site.scripts"]}`),
		"plugin.wasm": []byte("\x00asm\x01\x00\x00\x00"),
	})
	theme := `{"name":"community-look","version":"1.0.0"` + menus + `}`
	r.publish(packages.KindTheme, "community-look", "1.0.0", map[string][]byte{"theme.json": []byte(theme)})
	b, err := os.ReadFile("testdata/community/kit.json")
	if err != nil {
		r.t.Fatal(err)
	}
	if edit != nil {
		var k packages.Kit
		if err := jsonx.Unmarshal(b, &k); err != nil {
			r.t.Fatal(err)
		}
		edit(&k)
		if b, err = jsonx.Marshal(k); err != nil {
			r.t.Fatal(err)
		}
	}
	r.publish(packages.KindKit, "community", "1.0.0", map[string][]byte{"kit.json": b})
}

// apply reads the kit from the registry, verifies it like the callers do, plans and applies.
func (r *kitRig) apply(a Accepts) (*Plan, *Result) {
	r.t.Helper()
	ctx := context.Background()
	raw, err := r.d.Packages.Fetch(ctx, packages.KindKit, "community")
	if err != nil {
		r.t.Fatal(err)
	}
	p, origin, err := r.mgr.VerifyKit(ctx, raw, packages.SourceRegistry)
	if err != nil {
		r.t.Fatal(err)
	}
	pl, err := BuildPlan(ctx, r.d, p, Options{Origin: origin})
	if err != nil {
		r.t.Fatal(err)
	}
	res, err := Apply(ctx, r.d, pl, a, ApplyOptions{Actor: "owner"})
	if err != nil {
		r.t.Fatalf("apply: %v (plan refusals %v)", err, pl.Refusals)
	}
	return pl, res
}

func (r *kitRig) page(slug string) *entity.Entity {
	r.t.Helper()
	e, err := r.d.Pages.GetPage(context.Background(), "page", slug)
	if err != nil {
		r.t.Fatal(err)
	}
	return e
}

func TestApplyOnRealServices(t *testing.T) {
	r := newKitRig(t)
	ctx := context.Background()
	r.publishFixture("", nil)

	avail, err := Available(ctx, r.d)
	if err != nil || len(avail) != 1 || avail[0].Name != "community" {
		t.Fatalf("available: %v %+v", err, avail)
	}

	pl, res := r.apply(Accepts{})
	if len(pl.Refusals) != 0 {
		t.Fatalf("refusals: %v", pl.Refusals)
	}
	want := map[string]int{"plugins": 1, "themes": 1, "schemas": 1, "pages": 4, "menus": 1}
	for k, n := range want {
		if res.Created[k] != n {
			t.Errorf("created[%s] = %d, want %d", k, res.Created[k], n)
		}
	}

	sc, err := r.ents.GetSchemaBySlug(ctx, "story")
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		URLPattern string `json:"url_pattern"`
		Comments   struct {
			Policy struct {
				Enabled bool `json:"enabled"`
			} `json:"policy"`
		} `json:"comments"`
	}
	if err := jsonx.Unmarshal(sc.Settings, &st); err != nil || st.URLPattern != "/item/{slug}" || st.Comments.Policy.Enabled {
		t.Fatalf("story settings %s (%v)", sc.Settings, err)
	}
	for _, slug := range []string{"latest", "newest", "submit", "guidelines"} {
		if e := r.page(slug); e == nil || e.Status != entity.StatusPublished {
			t.Errorf("page %s: %+v", slug, e)
		}
	}
	header, err := r.d.Menus.GetMenuByLocation(ctx, "header")
	if err != nil {
		t.Fatal(err)
	}
	if full, err := r.mnus.GetMenuWithItems(ctx, header.ID); err != nil || len(full.Items) != 3 {
		t.Fatalf("header items: %v %+v", err, full)
	}
	if v, ok, err := r.d.Packages.Installed(ctx, packages.KindPlugin, "community-plug"); err != nil || !ok || v != "1.2.0" {
		t.Errorf("plugin installed: %q %v %v", v, ok, err)
	}
	row, err := r.d.Applied.Get(ctx, "community")
	if err != nil || row.Version != "1.0.0" || row.AppliedBy != "owner" {
		t.Fatalf("applied row: %+v %v", row, err)
	}

	// Again: nothing is created and the row is untouched.
	_, res = r.apply(Accepts{})
	if res.Total() != 0 || res.Recorded {
		t.Errorf("second apply: %v recorded=%v", res.Created, res.Recorded)
	}
	again, err := r.d.Applied.Get(ctx, "community")
	if err != nil || !again.AppliedAt.Equal(row.AppliedAt) {
		t.Errorf("row changed: %+v -> %+v (%v)", row, again, err)
	}

	// The owner edits a page: re-applying keeps the edit.
	edited := "Latest, my way"
	home := r.page("latest")
	if _, err := r.ents.UpdateEntity(ctx, home.ID, nil, &entity.UpdateEntityInput{Name: &edited}); err != nil {
		t.Fatal(err)
	}
	r.apply(Accepts{})
	if got := r.page("latest"); got.Name != edited {
		t.Errorf("owner edit lost: %q", got.Name)
	}
}

func TestApplyKeepsAHeldMenuLocation(t *testing.T) {
	r := newKitRig(t)
	ctx := context.Background()
	r.publishFixture("", nil)
	loc := "header"
	if _, err := r.mnus.CreateMenu(ctx, &menu.CreateMenuInput{Name: "Mine", Slug: "mine", Location: &loc}); err != nil {
		t.Fatal(err)
	}
	_, res := r.apply(Accepts{})
	if res.Created["menus"] != 0 || !slices.Contains(res.Kept, "menu 'Header'") {
		t.Fatalf("created=%v kept=%v", res.Created, res.Kept)
	}
	if m, err := r.d.Menus.GetMenuByLocation(ctx, "header"); err != nil || m.Name != "Mine" {
		t.Errorf("held menu replaced: %+v %v", m, err)
	}
}

// A kit menu at a location the kit's own theme declares: activating the theme
// seeds that location first, so the kit's menu is kept and listed (ensure-only).
func TestApplyKitMenuAtAThemeLocationIsKeptAfterActivation(t *testing.T) {
	r := newKitRig(t)
	ctx := context.Background()
	r.publishFixture(`,"menus":{"rail":{"title":"Rail","default":[{"label":"Home","url":"/"}]}}`, func(k *packages.Kit) {
		k.Menus[0].Location, k.Menus[0].Name = "rail", "Rail"
	})
	pl, res := r.apply(Accepts{ActivateTheme: true})
	if len(pl.Refusals) != 0 || pl.Menus[0].Action != ActionCreate {
		t.Fatalf("the plan offers the theme's location: %+v %v", pl.Menus, pl.Refusals)
	}
	if res.Created["menus"] != 0 || !slices.Contains(res.Kept, "menu 'Rail'") {
		t.Fatalf("created=%v kept=%v", res.Created, res.Kept)
	}
	m, err := r.d.Menus.GetMenuByLocation(ctx, "rail")
	if err != nil {
		t.Fatal(err)
	}
	if full, err := r.mnus.GetMenuWithItems(ctx, m.ID); err != nil || len(full.Items) != 1 || !strings.Contains(full.Items[0].Label, "Home") {
		t.Errorf("the theme's default menu stays: %+v %v", full, err)
	}
	if name, err := r.d.Packages.ActiveTheme(ctx); err != nil || name != "community-look" {
		t.Errorf("active theme %q %v", name, err)
	}
}
