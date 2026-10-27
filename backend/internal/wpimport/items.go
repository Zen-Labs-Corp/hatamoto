package wpimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/author"
	"hatamoto.dev/core/internal/entity"
)

func (r *Runner) keyPrefix() string { return KeyPrefix(r.dump.Site.URL) }

func (r *Runner) key(id string) string { return r.keyPrefix() + id }

// selectItems returns the items to import, each parent before its children.
func (r *Runner) selectItems() []*Item {
	pp := ""
	if r.dump.Site.PostsPageID != 0 {
		pp = fmt.Sprint(r.dump.Site.PostsPageID) // the posts page is the blog archive, not a page
	}
	byID := map[string]*Item{}
	var all []*Item
	for i := range r.dump.Items {
		it := &r.dump.Items[i]
		if it.Status == wpTrash || it.Status == "auto-draft" || it.Status == "inherit" || it.ID == pp {
			continue
		}
		if _, ok := r.schemaOf[it.Type]; !ok {
			continue
		}
		byID[it.ID] = it
		all = append(all, it)
	}
	depth := func(it *Item) int {
		d := 0
		for cur := it; cur.ParentID != "" && d < 10; d++ {
			p, ok := byID[cur.ParentID]
			if !ok {
				break
			}
			cur = p
		}
		return d
	}
	sort.SliceStable(all, func(i, j int) bool { return depth(all[i]) < depth(all[j]) })
	return all
}

// itemStatus maps a WordPress status; members-only content becomes a draft.
func itemStatus(it *Item) entity.EntityStatus {
	if it.Protected {
		return entity.StatusDraft
	}
	switch it.Status {
	case wpPublish:
		return entity.StatusPublished
	case wpFuture:
		if it.Date.After(time.Now()) {
			return entity.StatusScheduled
		}
		return entity.StatusPublished
	}
	return entity.StatusDraft // draft, pending, private
}

// body converts an item's HTML and rewrites its links. withEntities resolves
// internal links to imported entities (the second pass).
func (r *Runner) body(ctx context.Context, it *Item, withEntities bool) (string, Converted, RewriteStats) {
	conv := Convert(it.Content, r.dump.RawBodies)
	rw := &Rewriter{Hosts: r.hosts, Media: r.resolveMedia(ctx), Path: func(p string) (string, bool) {
		np, ok := r.termPaths[normPath(p)]
		return np, ok
	}}
	if withEntities {
		// Only entities in the site language are served at their path; on a
		// lookup error nothing resolves, which leaves the links as they were.
		site, _ := r.siteLanguage(ctx)
		rw.Entity = func(p string, q url.Values) (EntityRef, bool) { return r.entityFor(site, p, q) }
	}
	out, st := rw.Rewrite(conv.HTML)
	return entity.SanitizeRichText(out), conv, st
}

// entityFor resolves an old-site path or ?p=ID link to an imported entity in
// the site language. An entity in another locale is not public and shares its
// path with its site-language twin, so a link to it stays unresolved.
func (r *Runner) entityFor(site, p string, q url.Values) (EntityRef, bool) {
	for _, k := range []string{"p", "page_id"} {
		if id := q.Get(k); id != "" {
			if info, ok := r.infos[id]; ok && info.ent != nil && info.ent.Locale == site {
				return EntityRef{ID: info.ent.ID, Path: info.newPath}, true
			}
			return EntityRef{}, false
		}
	}
	if info, ok := r.oldPaths[normPath(p)]; ok && info.ent != nil && info.ent.Locale == site && normPath(p) != "/" {
		return EntityRef{ID: info.ent.ID, Path: info.newPath}, true
	}
	return EntityRef{}, false
}

func decodeSlug(s string) string {
	if un, err := url.PathUnescape(s); err == nil {
		return un
	}
	return s
}

// siteLanguage is the language of the target site (the settings step has run;
// the site store caches the row).
func (r *Runner) siteLanguage(ctx context.Context) (string, error) {
	c, err := r.d.Site.Get(ctx)
	if err != nil {
		return "", err
	}
	return c.Language, nil
}

// buildItem makes the ImportItem of one WordPress item.
func (r *Runner) buildItem(ctx context.Context, it *Item, withEntities bool) (entity.ImportItem, RewriteStats, error) {
	content, _, st := r.body(ctx, it, withEntities)
	attrs := map[string]any{"content": content}
	if it.Excerpt != "" {
		attrs["excerpt"] = it.Excerpt
	}
	if it.FeaturedMedia != "" {
		if m, ok := r.media.byID[it.FeaturedMedia]; ok && m.PublicURL != nil {
			attrs["featured_image"] = *m.PublicURL
		}
	}
	if s := it.SEO; s.Title != "" || s.Description != "" || s.NoIndex || s.OGImage != "" {
		if s.Title != "" {
			attrs["meta_title"] = s.Title
		}
		if s.Description != "" {
			attrs["meta_description"] = s.Description
		}
		if s.NoIndex {
			attrs["no_index"] = true
		}
		if c := s.Canonical; c != "" && normPath(pathOf(c)) != normPath(pathOf(it.Link)) {
			attrs["seo"] = map[string]any{"canonical": c} // kept as data: Hatamoto renders its own canonical
		}
		if s.OGImage != "" {
			if IsWPContent(s.OGImage) {
				if u := r.mediaURL(ctx, s.OGImage); u != "" {
					attrs["og_image"] = u
				}
			} else {
				attrs["og_image"] = s.OGImage
			}
		}
	}
	if it.Type == "post" {
		var tags []string
		for _, t := range it.Terms {
			if t.Taxonomy != "post_tag" {
				continue
			}
			for _, term := range r.dump.Terms {
				if term.Taxonomy == t.Taxonomy && term.Slug == t.Slug {
					tags = append(tags, term.Name)
				}
			}
		}
		if len(tags) > 0 {
			attrs["tags"] = tags
		}
	}
	rawAttrs, err := json.Marshal(attrs)
	if err != nil {
		return entity.ImportItem{}, st, err
	}
	wp := map[string]any{"id": it.ID, "type": it.Type, "link": it.Link, "status": it.Status, "protected": it.Protected}
	if it.Lang != "" {
		wp["lang"] = it.Lang
	}
	meta, _ := json.Marshal(map[string]any{"wordpress": wp})
	site, err := r.siteLanguage(ctx)
	if err != nil {
		return entity.ImportItem{}, st, err
	}
	locale, warn := localeFor(it.Lang, site)
	if warn != "" && !withEntities {
		r.res.Warnings = append(r.res.Warnings, fmt.Sprintf("%s %s: %s", it.Type, it.ID, warn))
	}

	slug := decodeSlug(it.Slug)
	name := strings.TrimSpace(it.Title)
	if name == "" {
		name = firstNonEmpty(slug, "Untitled "+it.ID)
	}
	if slug == "" {
		slug = author.Slugify(name)
	}
	ii := entity.ImportItem{
		Key: r.key(it.ID), SchemaSlug: r.schemaOf[it.Type], Name: name, Slug: slug, Locale: locale, Status: itemStatus(it),
		Attributes: rawAttrs, Metadata: meta,
		CreatedAt: it.Date, UpdatedAt: it.Modified,
	}
	if ii.Status == entity.StatusPublished || ii.Status == entity.StatusScheduled {
		ii.PublishedAt = it.Date
	}
	if it.ParentID != "" {
		if _, ok := r.infos[it.ParentID]; ok || r.inSet(it.ParentID) {
			ii.ParentKey = r.key(it.ParentID)
		}
	}
	if id, ok := r.authorIDs[it.AuthorKey]; ok {
		ii.BylinePersonID = &id
	}
	return ii, st, nil
}

func (r *Runner) inSet(id string) bool {
	for i := range r.dump.Items {
		if r.dump.Items[i].ID == id && r.dump.Items[i].Status != wpTrash {
			if _, ok := r.schemaOf[r.dump.Items[i].Type]; ok {
				return true
			}
		}
	}
	return false
}

// importContent imports pages, posts and custom post types.
func (r *Runner) importContent(ctx context.Context) error {
	items := r.selectItems()
	r.say("content", "%d items", len(items))
	edited, err := r.st.EditedEntities(ctx)
	if err != nil {
		return err
	}
	var batch []entity.ImportItem
	var batchSrc []*Item
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		defer func() { batch, batchSrc = nil, nil }()
		return r.runBatch(ctx, batch, batchSrc)
	}
	for _, it := range items {
		if edited[it.ID] {
			label := fmt.Sprintf("%s %q (%s)", it.Type, it.Title, it.ID)
			if r.opts.KeepEdited {
				if err := r.keepEdited(ctx, it); err != nil {
					r.fail(it, err)
				} else {
					r.res.KeptEdits = append(r.res.KeptEdits, label)
				}
				continue
			}
			r.res.OverwrittenEdits = append(r.res.OverwrittenEdits, label)
		}
		ii, st, err := r.buildItem(ctx, it, false)
		if err != nil {
			r.fail(it, err)
			continue
		}
		r.res.MediaRewritten += st.MediaRewritten
		r.res.Unresolved = append(r.res.Unresolved, st.Unresolved...)
		r.infos[it.ID] = &entInfo{item: it, body: ii}
		batch, batchSrc = append(batch, ii), append(batchSrc, it)
		if len(batch) >= 500 {
			if err := flush(); err != nil {
				return err
			}
			r.say("content", "%d/%d", r.res.Created+r.res.Updated+r.res.Failed, len(items))
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if n := len(r.res.OverwrittenEdits); n > 0 {
		r.res.Warnings = append(r.res.Warnings, fmt.Sprintf("%d items edited in Hatamoto since the last import were overwritten with the WordPress version (see overwritten_edits; use --keep-edited to leave them alone)", n))
	}
	if n := len(r.res.KeptEdits); n > 0 {
		r.res.Warnings = append(r.res.Warnings, fmt.Sprintf("%d items edited in Hatamoto since the last import were left as they are (see kept_edits)", n))
	}
	return nil
}

// keepEdited adopts the entity an earlier run created for it, untouched.
func (r *Runner) keepEdited(ctx context.Context, it *Item) error {
	id, ok, err := r.st.Lookup(ctx, KindEntity, it.ID)
	if err != nil || !ok {
		return fmt.Errorf("edited item has no entity: %v", err)
	}
	ent, err := r.d.Entities.GetEntity(ctx, id)
	if err != nil {
		return err
	}
	if sc, err := r.d.Entities.GetSchema(ctx, ent.SchemaID); err == nil {
		ent.Schema = sc
	}
	r.byID[ent.ID] = ent
	r.infos[it.ID] = &entInfo{item: it, ent: ent} // no body: the link pass leaves it alone
	return nil
}

func (r *Runner) fail(it *Item, err error) {
	r.res.Failed++
	r.res.Errors = append(r.res.Errors, ItemError{Kind: it.Type, ID: it.ID, Title: it.Title, Err: err.Error()})
	delete(r.infos, it.ID)
}

// maxSlugTries is how many numbered slugs (slug-2, slug-3, ...) an imported
// item tries before it fails.
const maxSlugTries = 25

// runBatch imports one batch. An item whose slug is taken (WordPress allows
// one per date or parent, Hatamoto one per schema, and the site may have a page
// of that name already) is renamed slug-2, slug-3, ...: the imported item
// gives way, never the page that is there.
func (r *Runner) runBatch(ctx context.Context, batch []entity.ImportItem, src []*Item) error {
	pending, pendingSrc := batch, src
	base := make([]string, len(batch))
	for i := range batch {
		base[i] = batch[i].Slug
	}
	for try := 1; len(pending) > 0; try++ {
		res, err := r.d.Entities.Import(ctx, r.opts.Actor, entity.ImportBatch{Source: SourceName, Items: pending})
		if err != nil {
			return err
		}
		var next []entity.ImportItem
		var nextSrc []*Item
		var nextBase []string
		for i, ir := range res.Items {
			it := pendingSrc[i]
			switch {
			case ir.Err != nil && errors.Is(ir.Err, entity.ErrSlugAlreadyExists) && try < maxSlugTries:
				b := pending[i]
				b.Slug = fmt.Sprintf("%s-%d", base[i], try+1)
				next, nextSrc, nextBase = append(next, b), append(nextSrc, it), append(nextBase, base[i])
			case ir.Err != nil:
				r.fail(it, ir.Err)
			default:
				r.record(ctx, it, ir, pending[i])
			}
		}
		pending, pendingSrc, base = next, nextSrc, nextBase
	}
	return nil
}

func (r *Runner) record(ctx context.Context, it *Item, ir entity.ImportItemResult, ii entity.ImportItem) {
	if ir.Created {
		r.res.Created++
	} else {
		r.res.Updated++
	}
	ent, err := r.d.Entities.GetEntity(ctx, ir.ID)
	if err != nil {
		r.fail(it, err)
		return
	}
	if sc, err := r.schema(ctx, ii.SchemaSlug); err == nil {
		ent.Schema = sc
	}
	r.byID[ent.ID] = ent
	if info, ok := r.infos[it.ID]; ok {
		info.ent, info.body = ent, ii
	}
	if err := r.st.Put(ctx, r.run.ID, KindEntity, it.ID, ent.ID, stamp(ent.UpdatedAt)); err != nil {
		r.res.Warnings = append(r.res.Warnings, "id map: "+err.Error())
	}
	r.assignTerms(ctx, it, ent)
}

// assignTerms replaces the entity's terms with the item's categories and custom terms.
func (r *Runner) assignTerms(ctx context.Context, it *Item, ent *entity.Entity) {
	var ids []uuid.UUID
	for _, t := range it.Terms {
		if id, ok := r.termIDs[t.Taxonomy+":"+t.Slug]; ok {
			ids = append(ids, id)
		}
	}
	if len(ids) > 64 {
		ids = ids[:64]
	}
	if err := r.d.Taxonomy.ReplaceEntityTerms(ctx, ent.ID, ids); err != nil {
		r.res.Warnings = append(r.res.Warnings, fmt.Sprintf("terms of %q: %v", it.Title, err))
	}
}

// pathOfEntity is the public path of an imported entity.
func (r *Runner) pathOfEntity(ctx context.Context, snap *entity.URLSnapshot, ent *entity.Entity) string {
	var ancestors []string
	cur := ent
	for i := 0; i < 10 && cur.ParentID != nil; i++ {
		p, ok := r.byID[*cur.ParentID]
		if !ok {
			var err error
			if p, err = r.d.Entities.GetEntity(ctx, *cur.ParentID); err != nil {
				break
			}
			r.byID[p.ID] = p
		}
		ancestors = append([]string{p.Slug}, ancestors...)
		cur = p
	}
	return snap.EntityPath(ent, ancestors)
}

// rewriteLinks computes each entity's new path, then redoes the bodies that
// link to other pages so those links point at entities (data-entity-id).
func (r *Runner) rewriteLinks(ctx context.Context) error {
	snap, err := r.d.Entities.URLRegistry().Snapshot(ctx)
	if err != nil {
		return err
	}
	for _, info := range r.infos {
		if info.ent == nil {
			continue
		}
		info.newPath = r.pathOfEntity(ctx, snap, info.ent)
		if p := normPath(pathOf(info.item.Link)); info.item.Link != "" && p != "" {
			if !strings.Contains(info.item.Link, "?p=") && !strings.Contains(info.item.Link, "?page_id=") {
				r.oldPaths[p] = info
			}
		}
	}
	var batch []entity.ImportItem
	var src []*Item
	for _, info := range r.infos {
		if info.ent == nil || !strings.Contains(attrOf(info.body, "content"), "href=") {
			continue
		}
		ii, st, err := r.buildItem(ctx, info.item, true)
		if err != nil {
			continue
		}
		r.res.LinksRewritten += st.LinksRewritten
		ii.Slug = info.ent.Slug // a slug retry must keep its suffix
		batch, src = append(batch, ii), append(src, info.item)
	}
	r.say("links", "%d bodies with links", len(batch))
	for len(batch) > 0 {
		n := min(len(batch), 500)
		res, err := r.d.Entities.Import(ctx, r.opts.Actor, entity.ImportBatch{Source: SourceName, Items: batch[:n]})
		if err != nil {
			return err
		}
		for i, ir := range res.Items {
			if ir.Err != nil {
				r.res.Errors = append(r.res.Errors, ItemError{Kind: "links", ID: src[i].ID, Title: src[i].Title, Err: ir.Err.Error()})
				continue
			}
			// The rewrite is an import write too: stamp it, so it does not look like an owner edit.
			if ent, err := r.d.Entities.GetEntity(ctx, ir.ID); err == nil {
				if err := r.st.Put(ctx, r.run.ID, KindEntity, src[i].ID, ent.ID, stamp(ent.UpdatedAt)); err != nil {
					r.res.Warnings = append(r.res.Warnings, "id map: "+err.Error())
				}
			}
		}
		batch, src = batch[n:], src[n:]
	}
	return nil
}

// attrOf reads a string attribute of an import item.
func attrOf(ii entity.ImportItem, key string) string {
	var m map[string]any
	if json.Unmarshal(ii.Attributes, &m) != nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}
