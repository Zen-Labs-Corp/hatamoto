package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/indexnow"
	"hatamoto.dev/core/internal/seo"
)

// Item kinds the sink queues.
const (
	inKindPublished   = "published"   // the entity is live: ping its URL
	inKindUnpublished = "unpublished" // the row still exists but left public: ping the old URL
	inKindDeleted     = "deleted"     // Item.Entity carries the pre-delete copy
	inKindImport      = "import"      // Item.IDs are the stored entities
)

// indexNowSink turns entity events into IndexNow items. Emit never blocks.
type indexNowSink struct{ r *indexnow.Runner }

func (k indexNowSink) Emit(_ context.Context, ev entity.Event) {
	if it, ok := indexNowItem(ev); ok {
		k.r.Notify(it)
	}
}

// indexNowItem applies the ping rules: publish and edits of a live entity
// ping; leaving "published" and deleting a live entity ping the old URL; draft
// work pings nothing.
func indexNowItem(ev entity.Event) (indexnow.Item, bool) {
	switch ev.Kind {
	case entity.EventPublished:
		return indexnow.Item{Kind: inKindPublished, ID: ev.ID}, true
	case entity.EventUpdated:
		switch {
		case ev.Status == string(entity.StatusPublished):
			return indexnow.Item{Kind: inKindPublished, ID: ev.ID}, true
		case ev.PrevStatus == string(entity.StatusPublished):
			return indexnow.Item{Kind: inKindUnpublished, ID: ev.ID}, true
		}
	case entity.EventDeleted:
		if ev.Entity != nil && ev.Entity.Status == entity.StatusPublished {
			return indexnow.Item{Kind: inKindDeleted, ID: ev.ID, Entity: ev.Entity}, true
		}
	case entity.EventImportCompleted:
		if ev.Import != nil && len(ev.Import.IDs) > 0 {
			return indexnow.Item{Kind: inKindImport, IDs: ev.Import.IDs}, true
		}
	}
	return indexnow.Item{}, false
}

// notifyIndexNowPublished queues a scheduled publish, which bypasses the
// entity service.
func (s *Server) notifyIndexNowPublished(id uuid.UUID) {
	if s.indexNow != nil {
		s.indexNow.Notify(indexnow.Item{Kind: inKindPublished, ID: id})
	}
}

// indexNowResolver maps items to public URLs. The dependencies are functions
// so the rules are testable without a database.
type indexNowResolver struct {
	settings func(ctx context.Context) *seo.Settings
	baseURL  func(ctx context.Context) string
	get      func(ctx context.Context, id uuid.UUID) (*entity.Entity, error)
	path     func(ctx context.Context, e *entity.Entity) string
	// localeHidden reports a configured locale that is not public; nil means none.
	localeHidden func(ctx context.Context, locale string) bool
	// paused reports a site that is not open to visitors (site access): no pings.
	paused   func(ctx context.Context) bool
	warnBase func() // called when IndexNow is on but no base URL is known
}

func (s *Server) newIndexNowResolver() *indexNowResolver {
	return &indexNowResolver{
		settings: s.seoSettings,
		baseURL: func(ctx context.Context) string {
			if s.cfg != nil {
				if b := strings.TrimRight(strings.TrimSpace(s.cfg.PublicSite.BaseURL), "/"); b != "" {
					return b
				}
			}
			if s.siteConfig != nil {
				if c, err := s.siteConfig.Get(ctx); err == nil {
					return strings.TrimRight(strings.TrimSpace(c.PublicBaseURL), "/")
				}
			}
			return ""
		},
		get: s.entityRepo.GetByID,
		path: func(ctx context.Context, e *entity.Entity) string {
			return locateEntity(ctx, s.urlSnapshot(ctx), entity.NewPostgresPublicRepository(s.db), e).Path
		},
		localeHidden: func(ctx context.Context, locale string) bool {
			return s.urlSnapshot(ctx).Locales().IsHidden(locale)
		},
		paused: func(ctx context.Context) bool { return s.siteAccessSettings(ctx).On() },
		warnBase: func() {
			s.indexNowBaseWarn.Do(func() {
				s.logger.Warn("indexnow: no public base URL is set; pings are off until one is")
			})
		},
	}
}

func (r *indexNowResolver) Config(ctx context.Context) (indexnow.Config, bool) {
	if r.paused != nil && r.paused(ctx) {
		return indexnow.Config{}, false
	}
	set := r.settings(ctx)
	if set == nil || !set.IndexNow.Enabled || set.IndexNow.Key == "" {
		return indexnow.Config{}, false
	}
	base := r.baseURL(ctx)
	if base == "" {
		r.warnBase()
		return indexnow.Config{}, false
	}
	return indexnow.Config{Key: set.IndexNow.Key, Endpoint: set.IndexNow.Host(), Base: base}, true
}

func (r *indexNowResolver) URLs(ctx context.Context, it indexnow.Item) []string {
	set := r.settings(ctx)
	cfg, ok := r.Config(ctx)
	if !ok {
		return nil
	}
	var ents []*entity.Entity
	switch it.Kind {
	case inKindDeleted:
		if e, ok := it.Entity.(*entity.Entity); ok {
			ents = append(ents, e)
		}
	case inKindPublished, inKindUnpublished:
		if e, err := r.get(ctx, it.ID); err == nil {
			// A queued publish that was reverted meanwhile is not a live page.
			if it.Kind == inKindUnpublished || e.Status == entity.StatusPublished {
				ents = append(ents, e)
			}
		}
	case inKindImport:
		for _, id := range it.IDs {
			if e, err := r.get(ctx, id); err == nil && e.Status == entity.StatusPublished {
				ents = append(ents, e)
			}
		}
	}
	var out []string
	for _, e := range ents {
		if !indexNowEligible(set, e) || (r.localeHidden != nil && r.localeHidden(ctx, e.Locale)) {
			continue
		}
		if p := r.path(ctx, e); p != "" && !set.Sitemap.ExcludedPath(p) {
			out = append(out, cfg.Base+p)
		}
	}
	return out
}

// indexNowEligible mirrors the sitemap's filters: a routable schema, an entry
// search engines may index, not hidden from the sitemap and not excluded.
func indexNowEligible(set *seo.Settings, e *entity.Entity) bool {
	if e == nil || e.Slug == "" || e.Schema == nil || entity.IsReuseSchema(e.Schema) || entity.IsBlockSchema(e.Schema) {
		return false
	}
	if seo.EntryNoIndex(e.Metadata, e.Attributes) || entity.FlattenAttributes(e.Attributes)["hide_from_sitemap"] == "true" {
		return false
	}
	kind := seo.KindSingle
	if e.Schema.Slug == entity.PersonSchemaSlug {
		// Like the sitemap: a person page exists only when marked public, and
		// the owner may leave author pages out.
		if entity.FlattenAttributes(e.Attributes)["public"] != "true" || set.Sitemap.NoAuthor {
			return false
		}
		kind = seo.KindAuthor
	}
	return !set.NoIndexView(string(kind), e.Schema.Slug, "")
}

// indexNowKeyFile answers GET /<key>.txt while IndexNow is on: the key as
// plain text. name is the single path segment.
func (s *Server) indexNowKeyFile(ctx context.Context, name string) (string, bool) {
	set := s.seoSettings(ctx)
	if set == nil || !set.IndexNow.Enabled || set.IndexNow.Key == "" || name != set.IndexNow.Key+".txt" {
		return "", false
	}
	return set.IndexNow.Key, true
}

func serveKeyFile(w http.ResponseWriter, key string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// The key changes when it is renewed or IndexNow is switched off; a shared cache must not keep serving it.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(key))
}
