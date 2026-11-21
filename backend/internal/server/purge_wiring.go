package server

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/cdnpurge"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/purgestate"
)

// Item kinds the purge sink queues.
const (
	pgKindPublished   = "published"   // the entity is live: its cached copy is stale
	pgKindUnpublished = "unpublished" // the row still exists but left public: the old URL is stale
	pgKindDeleted     = "deleted"     // Item.Entity carries the pre-delete copy
	pgKindImport      = "import"      // Item.IDs are the stored entities
)

// purgeSink turns entity events into purge items. Emit never blocks.
type purgeSink struct{ r *cdnpurge.Runner }

func (k purgeSink) Emit(_ context.Context, ev entity.Event) {
	if it, ok := purgeItem(ev); ok {
		k.r.Notify(it)
	}
}

// purgeItem applies the same rules as the search ping: publish and edits of a
// live entity purge; leaving "published" and deleting a live entity purge the
// old URL; draft work purges nothing.
func purgeItem(ev entity.Event) (cdnpurge.Item, bool) {
	switch ev.Kind {
	case entity.EventPublished:
		return cdnpurge.Item{Kind: pgKindPublished, ID: ev.ID}, true
	case entity.EventUpdated:
		switch {
		case ev.Status == string(entity.StatusPublished):
			return cdnpurge.Item{Kind: pgKindPublished, ID: ev.ID}, true
		case ev.PrevStatus == string(entity.StatusPublished):
			return cdnpurge.Item{Kind: pgKindUnpublished, ID: ev.ID}, true
		}
	case entity.EventDeleted:
		if ev.Entity != nil && ev.Entity.Status == entity.StatusPublished {
			return cdnpurge.Item{Kind: pgKindDeleted, ID: ev.ID, Entity: ev.Entity}, true
		}
	case entity.EventImportCompleted:
		if ev.Import != nil && len(ev.Import.IDs) > 0 {
			return cdnpurge.Item{Kind: pgKindImport, IDs: ev.Import.IDs}, true
		}
	}
	return cdnpurge.Item{}, false
}

// newPurgeRunner returns nil (no goroutine, no queue) unless
// HATAMOTO_PURGE_URL is set or a module registered a purger.
func (s *Server) newPurgeRunner() *cdnpurge.Runner {
	var targets []cdnpurge.Target
	if s.cfg.Purge.URL != "" {
		targets = append(targets, cdnpurge.NewWebhook(cdnpurge.WebhookOptions{
			URL: s.cfg.Purge.URL, Token: s.cfg.Purge.Token, AllowPrivate: s.cfg.Purge.AllowPrivate,
		}))
	}
	if name, p, ok := purgestate.Registered(); ok {
		targets = append(targets, registeredPurger{name: name, p: p})
	}
	if len(targets) == 0 {
		return nil
	}
	return cdnpurge.New(s.newPurgeResolver(), targets, s.logger, cdnpurge.Options{})
}

// registeredPurger adapts a module's purger to a runner target. A returned
// error is retried, never surfaced to the writer.
type registeredPurger struct {
	name string
	p    purgestate.Purger
}

func (r registeredPurger) Name() string { return r.name }

func (r registeredPurger) Send(ctx context.Context, req cdnpurge.Request) error {
	if err := r.p.Purge(ctx, purgestate.Request{URLs: slices.Clone(req.URLs), All: req.All}); err != nil {
		return cdnpurge.Retryable{Err: err}
	}
	return nil
}

// purgeResolver maps items to absolute public URLs. The dependencies are
// functions so the rules are testable without a database. Unlike the search
// ping it applies no noindex or sitemap filter: every public page is cached.
type purgeResolver struct {
	baseURL      func(ctx context.Context) string
	get          func(ctx context.Context, id uuid.UUID) (*entity.Entity, error)
	locate       func(ctx context.Context, e *entity.Entity) entity.EntityLocation
	localeHidden func(ctx context.Context, locale string) bool // nil means none hidden
	warnBase     func()                                        // called when no base URL is known
}

func (s *Server) newPurgeResolver() *purgeResolver {
	return &purgeResolver{
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
		get: func(ctx context.Context, id uuid.UUID) (*entity.Entity, error) {
			return s.entityRepo.GetByID(ctx, id)
		},
		locate: func(ctx context.Context, e *entity.Entity) entity.EntityLocation {
			return locateEntity(ctx, s.urlSnapshot(ctx), entity.NewPostgresPublicRepository(s.db), e)
		},
		localeHidden: func(ctx context.Context, locale string) bool {
			return s.urlSnapshot(ctx).Locales().IsHidden(locale)
		},
		warnBase: func() {
			s.purgeBaseWarn.Do(func() {
				s.logger.Warn("cdnpurge: no public base URL is set; purges are off until one is")
			})
		},
	}
}

func (r *purgeResolver) URLs(ctx context.Context, it cdnpurge.Item) []string {
	if len(it.Paths) == 0 && it.Kind == "" {
		return nil
	}
	base := r.baseURL(ctx)
	if base == "" {
		r.warnBase()
		return nil
	}
	var out []string
	for _, p := range it.Paths {
		out = append(out, base+p)
	}
	var ents []*entity.Entity
	switch it.Kind {
	case pgKindDeleted:
		if e, ok := it.Entity.(*entity.Entity); ok {
			ents = append(ents, e)
		}
	case pgKindPublished, pgKindUnpublished:
		if e, err := r.get(ctx, it.ID); err == nil {
			// A queued publish that was reverted meanwhile is not a live page.
			if it.Kind == pgKindUnpublished || e.Status == entity.StatusPublished {
				ents = append(ents, e)
			}
		}
	case pgKindImport:
		for _, id := range it.IDs {
			if e, err := r.get(ctx, id); err == nil && e.Status == entity.StatusPublished {
				ents = append(ents, e)
			}
		}
	}
	for _, e := range ents {
		if e == nil || e.Slug == "" || e.Schema == nil || entity.IsReuseSchema(e.Schema) || entity.IsBlockSchema(e.Schema) {
			continue
		}
		if r.localeHidden != nil && r.localeHidden(ctx, e.Locale) {
			continue
		}
		loc := r.locate(ctx, e)
		if loc.Path != "" {
			out = append(out, base+loc.Path)
		}
		if loc.ArchivePath != "" {
			out = append(out, base+loc.ArchivePath)
		}
	}
	return out
}

// notifyPurgePublished queues a scheduled publish, which bypasses the entity
// service.
func (s *Server) notifyPurgePublished(id uuid.UUID) {
	if s.purge != nil {
		s.purge.Notify(cdnpurge.Item{Kind: pgKindPublished, ID: id})
	}
}

// notifyPurgePaths queues stale site paths (for example the old URLs of a move).
func (s *Server) notifyPurgePaths(paths ...string) {
	if s.purge != nil && len(paths) > 0 {
		s.purge.Notify(cdnpurge.Item{Paths: paths})
	}
}

// notifyPurgeAll queues a purge of every cached page (Clear cache).
func (s *Server) notifyPurgeAll() {
	if s.purge != nil {
		s.purge.Notify(cdnpurge.Item{All: true})
	}
}
