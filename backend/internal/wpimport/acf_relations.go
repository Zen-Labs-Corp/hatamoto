package wpimport

import (
	"context"
	"fmt"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

// linksStep rewrites the links of the bodies, then writes the relationship
// fields, now that every imported item has an entity.
func (r *Runner) linksStep(ctx context.Context) error {
	if err := r.rewriteLinks(ctx); err != nil {
		return err
	}
	return r.acfRelations(ctx)
}

// acfRelations writes the relationship fields of every imported item (and the
// repeater and flexible content rows that hold them) as a partial update, so
// nothing else of the entity changes. Related items that were not imported
// are left out. A write that changes nothing is not stamped again.
func (r *Runner) acfRelations(ctx context.Context) error {
	if len(r.acf) == 0 || r.res.ACF == nil {
		return nil
	}
	var batch []entity.ImportItem
	var src []*Item
	for _, info := range r.infos {
		// A kept edit has no body: its entity stays as it is.
		if info.ent == nil || len(info.body.Attributes) == 0 {
			continue
		}
		ii, ok := r.relationItem(ctx, info)
		if !ok {
			continue
		}
		batch, src = append(batch, ii), append(src, info.item)
	}
	r.say("links", "%d items with related items", len(batch))
	for len(batch) > 0 {
		n := min(len(batch), 500)
		res, err := r.d.Entities.Import(ctx, r.opts.Actor, entity.ImportBatch{Source: SourceName, Items: batch[:n]})
		if err != nil {
			return err
		}
		for i, ir := range res.Items {
			switch {
			case ir.Err != nil:
				r.res.Errors = append(r.res.Errors, ItemError{Kind: "acf", ID: src[i].ID, Title: src[i].Title, Err: ir.Err.Error()})
			case ir.Unchanged:
				r.res.ACF.RelationsUnchanged++
			default:
				r.res.ACF.RelationsChanged++
				// The write is an import write: stamp it, so it does not look like an owner edit.
				if ent, err := r.d.Entities.GetEntity(ctx, ir.ID); err == nil {
					if err := r.st.Put(ctx, r.run.ID, KindEntity, src[i].ID, ent.ID, stamp(ent.UpdatedAt)); err != nil {
						r.res.Warnings = append(r.res.Warnings, "id map: "+err.Error())
					}
				}
			}
		}
		batch, src = batch[n:], src[n:]
	}
	return nil
}

// relationItem builds the partial update of one item: only its relationship
// bearing properties, fully rebuilt with entity ids. ok is false when the item
// has none.
func (r *Runner) relationItem(ctx context.Context, info *entInfo) (entity.ImportItem, bool) {
	it := info.item
	rd := r.newACFReader(ctx, it, true, false)
	attrs := map[string]any{}
	var props []string
	for _, m := range r.acf[it.Type] {
		if !acfRelationBearing(m) {
			continue
		}
		rd.field(m, attrs)
		if _, ok := attrs[m.Prop]; !ok {
			continue
		}
		props = append(props, m.Prop)
		if l := r.acfLine(m, it.Type); l != nil {
			l.Items++
			l.Rows += rd.rows
			l.Links += rd.links
		}
	}
	if len(props) == 0 {
		return entity.ImportItem{}, false
	}
	raw, err := jsonx.Marshal(attrs)
	if err != nil {
		r.res.Errors = append(r.res.Errors, ItemError{Kind: "acf", ID: it.ID, Title: it.Title, Err: fmt.Sprintf("related items: %v", err)})
		return entity.ImportItem{}, false
	}
	return entity.ImportItem{
		Key: r.key(it.ID), TargetID: &info.ent.ID, SchemaSlug: r.schemaOf[it.Type], Name: info.ent.Name,
		Partial: true, ReplacePaths: props, Attributes: raw,
	}, true
}
