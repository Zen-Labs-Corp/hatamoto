//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/admin/starter"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/render"
)

// The one-time upgrade seed and the new-site seed (needs a migrated Postgres):
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run MenuSeed
func TestMenuSeedIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping menu seed integration test")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "ms-it")
	t.Setenv("HATAMOTO_SITE_NAME", "MS IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "MS IT")
	t.Setenv("PUBLIC_BASE_URL", "https://ms-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pcfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	pcfg.MaxConns, pcfg.MinConns = 12, 1
	db, err := database.NewWithConfig(ctx, pcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))

	sfx := uuid.NewString()[:6]
	insert := func(schema, name, slug, attrs string) {
		exec(`INSERT INTO hatamoto_entities (schema_id,name,slug,status,published_at,attributes)
			SELECT id, $2::text, $3::text, 'published', now(), $4::jsonb FROM hatamoto_entity_schemas WHERE slug = $1`,
			schema, name, slug, attrs)
	}
	insert("page", "Alpha", "ms-alpha-"+sfx, `{}`)
	insert("page", "Hidden", "ms-hidden-"+sfx, `{"hide_from_nav":true}`)
	insert("page", "Gamma", "ms-gamma-"+sfx, `{"hide_from_nav":false}`)
	insert("post", "A post", "ms-post-"+sfx, `{}`)
	reset := func(setupDone bool) {
		t.Helper()
		exec(`DELETE FROM hatamoto_menus`)
		exec(`INSERT INTO hatamoto_site_config (id, company_name) VALUES (true, 'Seed test') ON CONFLICT (id) DO NOTHING`)
		exec(`UPDATE hatamoto_site_config SET menus_seeded_at = NULL,
			setup_completed_at = CASE WHEN $1 THEN now() ELSE NULL END`, setupDone)
		srv.siteConfig.Invalidate()
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_menus`)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE slug LIKE $1`, "ms-%-"+sfx)
	})
	headerMenus := func() []*menu.Menu {
		t.Helper()
		var out []*menu.Menu
		rows, err := db.Pool().Query(ctx, `SELECT id FROM hatamoto_menus WHERE location = 'header'`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			m, err := srv.menuService.GetMenuWithItems(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, m)
		}
		return out
	}
	marked := func() bool {
		t.Helper()
		srv.siteConfig.Invalidate()
		c, err := srv.siteConfig.Get(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return c.MenusSeededAt != nil
	}

	t.Run("upgrade seeds the old fallback once", func(t *testing.T) {
		reset(true)
		if err := srv.seedUpgradeHeader(ctx); err != nil {
			t.Fatal(err)
		}
		ms := headerMenus()
		if len(ms) != 1 || ms[0].Slug != "header" || ms[0].Name != "Header" {
			t.Fatalf("header menus = %+v", ms)
		}
		got := srv.newNavLinks(ctx).items(ms[0].Items)
		var labels []string
		for _, n := range got {
			if strings.HasPrefix(n.Href, "/ms-") && !strings.HasSuffix(n.Href, sfx) {
				t.Errorf("unexpected href %q", n.Href)
			}
			labels = append(labels, n.Label)
		}
		joined := strings.Join(labels, ",")
		if !strings.Contains(joined, "Alpha") || !strings.Contains(joined, "Gamma") || strings.Contains(joined, "Hidden") {
			t.Errorf("labels = %s", joined)
		}
		if last := ms[0].Items[len(ms[0].Items)-1]; last.Label != "Blog" || last.LinkType != menu.LinkTypeArchive ||
			last.SchemaSlug == nil || *last.SchemaSlug != "post" || last.URL == nil || *last.URL == "" {
			t.Errorf("last item = %+v", last)
		}
		for _, it := range ms[0].Items[:len(ms[0].Items)-1] {
			if it.LinkType != menu.LinkTypeEntity || it.EntityID == nil {
				t.Errorf("page item = %+v", it)
			}
		}
		if !marked() {
			t.Error("marker not set")
		}

		// Second run and concurrent runs change nothing; deleting the menu is final.
		if err := srv.seedUpgradeHeader(ctx); err != nil {
			t.Fatal(err)
		}
		if len(headerMenus()) != 1 {
			t.Fatal("second run duplicated the header")
		}
		if err := srv.menuService.DeleteMenu(ctx, ms[0].ID); err != nil {
			t.Fatal(err)
		}
		if err := srv.seedUpgradeHeader(ctx); err != nil {
			t.Fatal(err)
		}
		if len(headerMenus()) != 0 {
			t.Fatal("deleted header was re-created")
		}
	})

	t.Run("concurrent runs leave one header", func(t *testing.T) {
		reset(true)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := srv.seedUpgradeHeader(ctx); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if n := len(headerMenus()); n != 1 {
			t.Fatalf("header menus = %d, want 1", n)
		}
	})

	t.Run("existing header is untouched and the marker set", func(t *testing.T) {
		reset(true)
		if _, _, err := srv.menuService.EnsureMenuAtLocation(ctx, menu.EnsureInput{Name: "Mine", Slug: "mine", Location: "header"}); err != nil {
			t.Fatal(err)
		}
		if err := srv.seedUpgradeHeader(ctx); err != nil {
			t.Fatal(err)
		}
		ms := headerMenus()
		if len(ms) != 1 || ms[0].Name != "Mine" || len(ms[0].Items) != 0 {
			t.Fatalf("header menus = %+v", ms)
		}
		if !marked() {
			t.Error("marker not set")
		}
	})

	t.Run("unfinished setup seeds nothing", func(t *testing.T) {
		reset(false)
		if err := srv.seedUpgradeHeader(ctx); err != nil {
			t.Fatal(err)
		}
		if len(headerMenus()) != 0 || marked() {
			t.Fatal("seeded before setup finished")
		}
	})

	t.Run("new site gets header and footer", func(t *testing.T) {
		reset(false)
		if err := srv.SeedNewSiteMenus(ctx); err != nil {
			t.Fatal(err)
		}
		if err := srv.SeedNewSiteMenus(ctx); err != nil {
			t.Fatal(err)
		}
		ms := headerMenus()
		if len(ms) != 1 || len(ms[0].Items) != 1 || ms[0].Items[0].LinkType != menu.LinkTypeArchive {
			t.Fatalf("header = %+v", ms)
		}
		f, err := srv.menuService.GetMenuByLocation(ctx, "footer")
		if err != nil || f.Slug != "footer" {
			t.Fatalf("footer = %+v, %v", f, err)
		}
		if !marked() {
			t.Error("marker not set")
		}
	})

	t.Run("starter then seed keeps the starter header order", func(t *testing.T) {
		reset(false)
		exec(`DELETE FROM hatamoto_entities WHERE slug IN ('home','about','contact') OR slug LIKE 'hello-%' OR slug LIKE 'welcome-%'`)
		inst := &starter.Installer{Entities: srv.entityService, Menus: srv.menuService, Forms: srv.formService, Site: srv.siteConfig}
		if _, err := inst.Install(ctx, starter.Options{SiteName: "T"}); err != nil {
			t.Fatal(err)
		}
		if err := srv.SeedNewSiteMenus(ctx); err != nil {
			t.Fatal(err)
		}
		ms := headerMenus()
		if len(ms) != 1 {
			t.Fatalf("header menus = %d", len(ms))
		}
		var got []string
		pos := map[int]bool{}
		for _, it := range ms[0].Items {
			got = append(got, it.Label)
			if pos[it.Position] {
				t.Errorf("position %d used twice", it.Position)
			}
			pos[it.Position] = true
		}
		if strings.Join(got, ",") != "Home,About,Blog,Contact" {
			t.Fatalf("header = %v", got)
		}
	})

	setTheme := func(t *testing.T, themeJSON string) {
		t.Helper()
		engine, err := render.New(render.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := engine.SetTheme(fstest.MapFS{"theme.json": {Data: []byte(themeJSON)}}); err != nil {
			t.Fatal(err)
		}
		srv.renderEngine = engine
		t.Cleanup(func() { srv.renderEngine = nil })
	}

	t.Run("new site: a title-only core declaration still gets the core menus", func(t *testing.T) {
		reset(false)
		setTheme(t, `{"name":"x","menus":{"header":{"title":"Main"},"footer":{"title":"Footer"}}}`)
		if err := srv.SeedNewSiteMenus(ctx); err != nil {
			t.Fatal(err)
		}
		if len(headerMenus()) != 1 {
			t.Fatal("header not seeded for a title-only declaration")
		}
		if f, err := srv.menuService.GetMenuByLocation(ctx, "footer"); err != nil || f.Slug != "footer" {
			t.Fatalf("footer = %+v, %v", f, err)
		}
		if !marked() {
			t.Error("marker not set")
		}
	})

	t.Run("new site: theme header defaults win over the core header", func(t *testing.T) {
		reset(false)
		setTheme(t, `{"name":"x","menus":{"header":{"title":"Main","default":[{"label":"Docs","url":"/docs"}]}}}`)
		if err := srv.SeedNewSiteMenus(ctx); err != nil {
			t.Fatal(err)
		}
		ms := headerMenus()
		if len(ms) != 1 || ms[0].Name != "Main" || len(ms[0].Items) != 1 || ms[0].Items[0].Label != "Docs" {
			t.Fatalf("header = %+v", ms)
		}
	})

	t.Run("header nav is the menu and nothing else", func(t *testing.T) {
		reset(true)
		links := srv.newNavLinks(ctx)
		if nav := srv.headerNav(ctx, links); len(nav) != 0 {
			t.Fatalf("no header menu: nav = %+v", nav)
		}
		if _, _, err := srv.menuService.EnsureMenuAtLocation(ctx, menu.EnsureInput{Name: "Header", Slug: "header", Location: "header"}); err != nil {
			t.Fatal(err)
		}
		if nav := srv.headerNav(ctx, links); len(nav) != 0 {
			t.Fatalf("empty header menu with published pages: nav = %+v", nav)
		}
		if _, found := srv.menuNavFound(ctx, "header", links); !found {
			t.Fatal("empty header menu reported as missing")
		}
		url := "/custom"
		m, _ := srv.menuService.GetMenuByLocation(ctx, "header")
		if _, err := srv.menuService.ReplaceMenu(ctx, m.ID, nil, []menu.ReplaceItem{{Label: "Custom", LinkType: menu.LinkTypeCustom, URL: &url, Parent: -1}}); err != nil {
			t.Fatal(err)
		}
		if nav := srv.headerNav(ctx, links); len(nav) != 1 || nav[0].Href != "/custom" {
			t.Fatalf("nav = %+v", nav)
		}
	})
}
