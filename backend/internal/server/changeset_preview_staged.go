package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
)

// stagedChangeset is a staged changeset held in memory so the public loaders
// can render the site as it would look once the changeset is published. It is
// attached to a context only by the changeset preview; every other request has
// no staged set and the loaders behave exactly as before. It never writes.
//
// Coverage: entity upserts (update and create) and deletes. Listings, related
// content, search and the sitemap keep reading live data.
type stagedChangeset struct {
	header changeset.Header
	// ents are the staged entities by id: updates carry the live row with the
	// staged fields merged, creates a synthetic id (see stagedID).
	ents map[uuid.UUID]*stagedEntity
	// byPath maps the full public path (locale prefix included) of a staged
	// entity to it.
	byPath  map[string]*stagedEntity
	deleted map[uuid.UUID]bool
	// items lists the previewable entity items in seq order.
	items []*stagedEntity
}

// stagedEntity is one entity item of the changeset as the staged set shows it.
type stagedEntity struct {
	Seq    int
	Ent    *entity.Entity
	Live   *entity.Entity // nil for a create
	Path   string         // full public path, "" when the schema has no routing
	Delete bool
}

type stagedKey struct{}

func withStaged(ctx context.Context, o *stagedChangeset) context.Context {
	return context.WithValue(ctx, stagedKey{}, o)
}

// stagedFrom is nil in every request that is not a changeset preview.
func stagedFrom(ctx context.Context) *stagedChangeset {
	o, _ := ctx.Value(stagedKey{}).(*stagedChangeset)
	return o
}

// stagedSource is what the staged set reads to resolve items: live rows and
// source-key mappings. All reads.
type stagedSource struct {
	entity    func(ctx context.Context, id uuid.UUID) (*entity.Entity, error)
	schema    func(ctx context.Context, slug string) (*entity.EntitySchema, error)
	mapped    func(ctx context.Context, origin, sourceKey string) (uuid.UUID, bool, error)
	snapshot  *entity.URLSnapshot
	ancestors func(ctx context.Context, parent uuid.UUID) []string
}

// stagedID is the id a created entity has in the preview: stable for the
// changeset and source key, never a real row.
func stagedID(runID uuid.UUID, sourceKey string) uuid.UUID {
	return uuid.NewSHA1(runID, []byte("changeset-preview:"+sourceKey))
}

// stagedEntityPayload is the part of an entity item the preview reads.
type stagedEntityPayload struct {
	Schema     string               `json:"schema"`
	Name       string               `json:"name"`
	Slug       string               `json:"slug"`
	Locale     string               `json:"locale"`
	Attributes json.RawMessage      `json:"attributes"`
	ParentID   *uuid.UUID           `json:"parent_id"`
	ParentKey  string               `json:"parent_key"`
	SEO        *changeset.EntitySEO `json:"seo"`
}

// newStagedChangeset builds the staged set from the items of h. An item that
// cannot be shown (unknown schema, missing target, bad payload) is left out:
// the preview then shows the live page for it.
func newStagedChangeset(ctx context.Context, h changeset.Header, items []changeset.StoredItem, src stagedSource) *stagedChangeset {
	o := &stagedChangeset{header: h, ents: map[uuid.UUID]*stagedEntity{}, byPath: map[string]*stagedEntity{}, deleted: map[uuid.UUID]bool{}}
	origin := changeset.Origin(h.Plugin)
	keys := map[string]uuid.UUID{} // source key -> id, for parent_key
	for _, it := range items {
		if it.Kind != changeset.KindEntity {
			continue
		}
		id, live, ok := o.target(ctx, src, origin, h.RunID, it)
		if !ok {
			continue
		}
		if it.SourceKey != "" {
			keys[it.SourceKey] = id
		}
		if it.Op == changeset.OpDelete {
			if live == nil {
				continue
			}
			se := &stagedEntity{Seq: it.Seq, Ent: live, Live: live, Delete: true, Path: o.livePath(ctx, src, live)}
			o.deleted[id] = true
			o.items = append(o.items, se)
			continue
		}
		var p stagedEntityPayload
		if json.Unmarshal(it.Payload, &p) != nil {
			continue
		}
		ent, ok := o.merge(ctx, src, live, id, p, keys)
		if !ok {
			continue
		}
		se := &stagedEntity{Seq: it.Seq, Ent: ent, Live: live}
		if sc := src.snapshot; sc != nil && ent.Schema != nil {
			if loc, found, err := sc.Locate(ent.Schema.Slug, entity.FlattenAttributes(ent.Attributes),
				sc.VarsFor(ent, o.ancestorSlugs(ctx, src, ent))); err == nil && found {
				se.Path = loc.Path
			}
		}
		o.ents[id] = se
		if se.Path != "" {
			o.byPath[se.Path] = se
		}
		o.items = append(o.items, se)
	}
	return o
}

// target resolves the entity an item refers to: its live row for an update or
// delete, the synthetic id for a create.
func (o *stagedChangeset) target(ctx context.Context, src stagedSource, origin string, runID uuid.UUID, it changeset.StoredItem) (uuid.UUID, *entity.Entity, bool) {
	id := uuid.Nil
	if it.TargetID != "" {
		parsed, err := uuid.Parse(it.TargetID)
		if err != nil {
			return uuid.Nil, nil, false
		}
		id = parsed
	} else if it.SourceKey != "" {
		if mapped, found, err := src.mapped(ctx, origin, it.SourceKey); err != nil {
			return uuid.Nil, nil, false
		} else if found {
			id = mapped
		}
	}
	if id == uuid.Nil {
		if it.Op == changeset.OpDelete || it.SourceKey == "" {
			return uuid.Nil, nil, false
		}
		return stagedID(runID, it.SourceKey), nil, true
	}
	live, err := src.entity(ctx, id)
	if err != nil || live == nil {
		return uuid.Nil, nil, false
	}
	return id, live, true
}

// merge returns the entity as the publish would leave it. Fields a payload
// leaves empty keep the live value, as the publish does.
func (o *stagedChangeset) merge(ctx context.Context, src stagedSource, live *entity.Entity, id uuid.UUID, p stagedEntityPayload, keys map[string]uuid.UUID) (*entity.Entity, bool) {
	var ent entity.Entity
	if live != nil {
		ent = *live
	} else {
		sc, err := src.schema(ctx, p.Schema)
		if err != nil || sc == nil {
			return nil, false
		}
		now := time.Now().UTC()
		ent = entity.Entity{ID: id, SchemaID: sc.ID, Schema: sc, CreatedAt: now, UpdatedAt: now}
		if set := localeSetOf(src.snapshot); set != nil && set.Multi() {
			ent.Locale = set.Default()
		}
	}
	if p.Name != "" {
		ent.Name = p.Name
	}
	if p.Slug != "" {
		ent.Slug = p.Slug
	} else if ent.Slug == "" {
		ent.Slug = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p.Name), " ", "-"))
	}
	if p.Locale != "" {
		ent.Locale = p.Locale
	}
	if p.Attributes != nil {
		ent.Attributes = p.Attributes
	}
	attrs, err := changeset.MergeSEO(ent.Attributes, p.SEO)
	if err != nil {
		return nil, false
	}
	ent.Attributes = attrs
	switch {
	case p.ParentID != nil:
		ent.ParentID = p.ParentID
	case p.ParentKey != "":
		parent, ok := keys[p.ParentKey]
		if !ok {
			return nil, false
		}
		ent.ParentID = &parent
	}
	ent.Status = entity.StatusPublished // staged drafts are shown: that is the point of a preview
	return &ent, true
}

func localeSetOf(snap *entity.URLSnapshot) *entity.LocaleSet {
	if snap == nil {
		return nil
	}
	return snap.Locales()
}

// ancestorSlugs are the slugs above ent, through staged parents first.
func (o *stagedChangeset) ancestorSlugs(ctx context.Context, src stagedSource, ent *entity.Entity) []string {
	if ent.ParentID == nil || ent.Schema == nil || !src.snapshot.NeedsAncestors(ent.Schema.Slug, entity.FlattenAttributes(ent.Attributes)) {
		return nil
	}
	if p, ok := o.ents[*ent.ParentID]; ok {
		return append(o.ancestorSlugs(ctx, src, p.Ent), p.Ent.Slug)
	}
	if src.ancestors == nil {
		return nil
	}
	return src.ancestors(ctx, *ent.ParentID)
}

// livePath is where a live entity is served today.
func (o *stagedChangeset) livePath(ctx context.Context, src stagedSource, live *entity.Entity) string {
	if src.snapshot == nil || live.Schema == nil {
		return ""
	}
	loc, found, err := src.snapshot.Locate(live.Schema.Slug, entity.FlattenAttributes(live.Attributes),
		src.snapshot.VarsFor(live, o.ancestorSlugs(ctx, src, live)))
	if err != nil || !found {
		return ""
	}
	return loc.Path
}

// entityByID is the staged entity with id, a copy the loader may modify.
func (o *stagedChangeset) entityByID(id uuid.UUID) *entity.Entity {
	se, ok := o.ents[id]
	if !ok {
		return nil
	}
	c := *se.Ent
	return &c
}

// resolvePath answers a staged entity's own path.
func (o *stagedChangeset) resolvePath(fullPath, localeFreePath string) *resolvedPage {
	se, ok := o.byPath[fullPath]
	if !ok || se.Ent.Schema == nil {
		return nil
	}
	return &resolvedPage{Kind: resolveKindEntity, Entity: se.Ent, Slug: se.Ent.Slug, SchemaSlugs: []string{se.Ent.Schema.Slug}, URL: localeFreePath}
}

// hides reports that a live page is not what the path shows once the
// changeset is published: the entity is deleted, or staged to live elsewhere.
func (o *stagedChangeset) hides(res *resolvedPage) bool {
	if res == nil || res.Entity == nil {
		return false
	}
	_, staged := o.ents[res.Entity.ID]
	return staged || o.deleted[res.Entity.ID]
}

// applyStagedEntity swaps a loaded live entity for its staged version, in
// place. A request without a staged set, or an entity the changeset does not
// touch, is left alone.
func applyStagedEntity(ctx context.Context, ent *entity.Entity) {
	o := stagedFrom(ctx)
	if o == nil {
		return
	}
	if staged := o.entityByID(ent.ID); staged != nil {
		*ent = *staged
	}
}

// errNoPreviewItem means the changeset has nothing the preview can show.
var errNoPreviewItem = errors.New("changeset has no previewable page")

// stagedByScope is the staged entity a scoped page load names, nil outside a
// changeset preview.
func stagedByScope(ctx context.Context, scope publicScope, scoped bool) *entity.Entity {
	o := stagedFrom(ctx)
	if o == nil || !scoped || scope.EntityID == uuid.Nil {
		return nil
	}
	return o.entityByID(scope.EntityID)
}

// resolveStagedOrLive is resolveEntity, except that inside a changeset preview
// a staged entity answers its own path first and a live page the changeset
// deletes or moves no longer answers.
func resolveStagedOrLive(ctx context.Context, repo resolveRepo, snap *entity.URLSnapshot, fullPath, path string) (*resolvedPage, error) {
	o := stagedFrom(ctx)
	if o == nil {
		return resolveEntity(ctx, repo, snap, path)
	}
	if res := o.resolvePath(fullPath, path); res != nil {
		return res, nil
	}
	res, err := resolveEntity(ctx, repo, snap, path)
	if err == nil && o.hides(res) {
		return nil, nil
	}
	return res, err
}
