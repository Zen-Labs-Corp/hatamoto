package server

import (
	"context"
	"encoding/json"
	"fmt"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/menu"
)

// upgradeHeaderItems is the header an upgraded site used to get at render
// time: the first 50 published pages (minus the ones hidden from the nav or
// without a public URL) and a Blog link when the post archive has posts. Pages
// become entity items, so their links stay live.
func (s *Server) upgradeHeaderItems(ctx context.Context) ([]menu.ReplaceItem, error) {
	snap, err := s.urlSnapshotOrErr(ctx)
	if err != nil {
		return nil, fmt.Errorf("url registry: %w", err)
	}
	links := s.newNavLinksWith(ctx, snap)
	if links.snap.Locales().Multi() {
		s.logger.Warn("menu seed: this site has several languages; the Header is seeded from the default-language pages and their links, for every language")
	}
	publicRepo := entity.NewPostgresPublicRepository(s.db)
	var items []menu.ReplaceItem

	pages, err := publicRepo.ListPublished(ctx, "page", 1, 50)
	if err != nil {
		return nil, fmt.Errorf("list pages: %w", err)
	}
	if pages != nil {
		for _, p := range pages.Items {
			var attrs map[string]interface{}
			if err := json.Unmarshal(p.Attributes, &attrs); err == nil {
				if hidden, ok := attrs["hide_from_nav"].(bool); ok && hidden {
					continue
				}
			}
			want, ok := links.page(p)
			if !ok {
				continue
			}
			id, slug, schema := p.ID, p.Slug, "page"
			// A live entity link is only right while the menu renders the href
			// the fallback showed; where it would not, keep that href.
			if links.menuHref(&menu.MenuItem{LinkType: menu.LinkTypeEntity, EntityID: &id, EntitySlug: &slug, EntitySchemaSlug: &schema}) != want {
				items = append(items, menu.ReplaceItem{Label: p.Name, LinkType: menu.LinkTypeCustom, URL: &want, Parent: -1})
				continue
			}
			items = append(items, menu.ReplaceItem{Label: p.Name, LinkType: menu.LinkTypeEntity, EntityID: &id, Parent: -1})
		}
	}

	if it, ok := s.blogArchiveItem(ctx, snap, publicRepo, true); ok {
		items = append(items, it)
	}
	return items, nil
}

// blogArchiveItem is the Blog link to the post archive; false when the post
// family has no archive path, or, with needPosts, no published post.
func (s *Server) blogArchiveItem(ctx context.Context, snap *entity.URLSnapshot, repo *entity.PostgresPublicRepository, needPosts bool) (menu.ReplaceItem, bool) {
	cfg, ok := snap.Config("post")
	if !ok || cfg.ArchivePath == "" {
		return menu.ReplaceItem{}, false
	}
	if needPosts {
		slugs := append([]string{cfg.SchemaSlug}, cfg.Aliases...)
		posts, err := repo.ListPublishedBySchemas(ctx, slugs, 1, 1)
		if err != nil || posts == nil || posts.Total == 0 {
			return menu.ReplaceItem{}, false
		}
	}
	// The schema keeps the link following a path change; the URL is what a
	// binary from before this release renders after a rollback.
	schema, path := cfg.SchemaSlug, cfg.ArchivePath
	return menu.ReplaceItem{Label: "Blog", LinkType: menu.LinkTypeArchive, SchemaSlug: &schema, URL: &path, Parent: -1}, true
}

// seedUpgradeHeader gives a site that predates real menus the Header menu the
// render-time fallback used to invent. It runs once: the marker is set when it
// finishes (also when there was nothing to seed or a header menu already
// exists), so a Header the owner deletes later is never re-created. A failure
// leaves the marker unset and the next start retries. A site whose setup has
// not finished is skipped: the new-site seed covers it.
func (s *Server) seedUpgradeHeader(ctx context.Context) error {
	if s.menuService == nil || s.siteConfig == nil || s.db == nil {
		return nil
	}
	cfg, err := s.siteConfig.Get(ctx)
	if err != nil {
		return fmt.Errorf("site config: %w", err)
	}
	if cfg.SetupCompletedAt == nil || cfg.MenusSeededAt != nil {
		return nil
	}
	items, err := s.upgradeHeaderItems(ctx)
	if err != nil {
		return err
	}
	if len(items) > 0 {
		if _, _, err := s.menuService.EnsureMenuAtLocation(ctx, menu.EnsureInput{
			Name: "Header", Slug: menu.LocationHeader, Location: menu.LocationHeader, Items: items,
		}); err != nil {
			return fmt.Errorf("ensure header menu: %w", err)
		}
	}
	return s.siteConfig.MarkMenusSeeded(ctx)
}

// SeedNewSiteMenus gives a new site its menus and sets the marker so the
// upgrade seed never runs on it. The active theme's declared defaults go first
// (they are normally seeded on activation already; this catches a theme that
// was active before the manager could seed), then the core Header (with a Blog
// link when the post archive exists) and an empty Footer. Each is created only
// where no menu holds the location, so a theme's defaults win and a theme that
// declares a core location without defaults still gets the core menu. Setup
// calls it once.
func (s *Server) SeedNewSiteMenus(ctx context.Context) error {
	if s.menuService == nil || s.siteConfig == nil {
		return nil
	}
	if s.renderEngine != nil {
		if _, err := s.menuService.SeedDecl(ctx, s.renderEngine.MenuLocations()); err != nil {
			return fmt.Errorf("seed theme menus: %w", err)
		}
	}
	snap, err := s.urlSnapshotOrErr(ctx)
	if err != nil {
		return fmt.Errorf("url registry: %w", err)
	}
	var items []menu.ReplaceItem
	if it, ok := s.blogArchiveItem(ctx, snap, entity.NewPostgresPublicRepository(s.db), false); ok {
		items = append(items, it)
	}
	if _, _, err := s.menuService.EnsureMenuAtLocation(ctx, menu.EnsureInput{
		Name: "Header", Slug: menu.LocationHeader, Location: menu.LocationHeader, Items: items,
	}); err != nil {
		return fmt.Errorf("ensure header menu: %w", err)
	}
	if _, _, err := s.menuService.EnsureMenuAtLocation(ctx, menu.EnsureInput{
		Name: "Footer", Slug: menu.LocationFooter, Location: menu.LocationFooter,
	}); err != nil {
		return fmt.Errorf("ensure footer menu: %w", err)
	}
	return s.siteConfig.MarkMenusSeeded(ctx)
}
