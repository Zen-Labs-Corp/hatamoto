package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/menu"
)

// hasEntities reports whether the site has an entry to copy.
func (a *Admin) hasEntities(ctx context.Context) bool {
	if a.deps.Content.Entities == nil || a.deps.Content.Copier == nil {
		return false
	}
	res, err := a.deps.Content.Entities.ListEntitiesWithOffset(ctx, &entity.ListEntitiesFilter{}, &entity.OffsetPage{Page: 1, Limit: 1})
	return err == nil && res.Total > 0
}

// startSources are the entries a new locale starts from: the home page, then
// the entries of the header menu in order, each once.
func (a *Admin) startSources(ctx context.Context) []uuid.UUID {
	var out []uuid.UUID
	seen := map[uuid.UUID]bool{}
	add := func(id uuid.UUID) {
		if id != uuid.Nil && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if cfg, err := a.loadSite(ctx); err == nil {
		add(cfg.HomepageEntityID)
		if cfg.HomepageEntityID == uuid.Nil && a.deps.Growth.Explain != nil {
			if ex, err := a.deps.Growth.Explain.ExplainURL(ctx, "/"); err == nil {
				if id, err := uuid.Parse(ex.Page.EntityID); err == nil {
					add(id)
				}
			}
		}
	}
	if ms := a.deps.Growth.Menus; ms != nil {
		if list, err := ms.ListMenus(ctx); err == nil {
			for _, m := range list {
				if m.Location == nil || *m.Location != menu.LocationHeader {
					continue
				}
				if full, err := ms.GetMenuWithItems(ctx, m.ID); err == nil {
					var walk func(items []*menu.MenuItem)
					walk = func(items []*menu.MenuItem) {
						for _, it := range items {
							if it.EntityID != nil {
								add(*it.EntityID)
							}
							walk(it.Children)
						}
					}
					walk(full.Items)
				}
				break
			}
		}
	}
	return out
}

// startWithCopies makes draft copies of the start sources in tag and returns
// the sentence for the flash. A failure never undoes the locale.
func (a *Admin) startWithCopies(r *http.Request, tag string) string {
	ctx := r.Context()
	set, ok := a.multiLocale(ctx)
	if !ok || a.deps.Content.Copier == nil {
		return ""
	}
	uid := uuid.Nil
	if u := actorID(r); u != nil {
		uid = *u
	}
	created, skipped, failed := 0, 0, 0
	for _, id := range a.startSources(ctx) {
		src, err := a.deps.Content.Entities.GetEntity(ctx, id)
		if err != nil || src.Locale != set.Default() {
			skipped++
			continue
		}
		cp, err := a.deps.Content.Copier.CreateCopy(ctx, uid, id, entity.CopyOptions{Locale: tag, DefaultLocale: set.Default(), CreateParents: true, AutoSlug: true, From: entity.CopyFromThis})
		switch {
		case errors.Is(err, entity.ErrLocaleTaken), errors.Is(err, entity.ErrCopyUnsupported):
			skipped++
		case err != nil:
			a.logger().Error("start with copies failed", "entity", id, "locale", tag, "err", err)
			failed++
		default:
			created++
			a.auditEntity(r, auditEntityCreateCopy, cp.ID, cp, map[string]any{"source": id.String(), "locale": tag, "start": true})
		}
	}
	if created > 0 {
		a.invalidate(ctx)
	}
	msg := fmt.Sprintf("%d draft copies created", created)
	if created == 1 {
		msg = "1 draft copy created"
	}
	if skipped > 0 {
		msg += fmt.Sprintf(", %d skipped", skipped)
	}
	if failed > 0 {
		msg += fmt.Sprintf(", %d failed", failed)
	}
	return msg + "."
}
