package server

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

var _ render.RefSource = (*renderData)(nil)

// Block returns the doc of a block entity (any status: blocks are always
// drafts). An id that is unknown, malformed or not a block is reported as
// missing. A failed lookup counts as a data failure so the page is not cached
// with a hole where the block belongs.
func (d *renderData) Block(ctx context.Context, id string) (*builder.Doc, bool) {
	return d.partDoc(ctx, entity.BlockSchemaSlug, "", id)
}

// Part returns the doc of a theme_part entity whose slot is slot.
func (d *renderData) Part(ctx context.Context, slot, id string) (*builder.Doc, bool) {
	return d.partDoc(ctx, entity.PartSchemaSlug, slot, id)
}

func (d *renderData) partDoc(ctx context.Context, schema, slot, id string) (*builder.Doc, bool) {
	if d == nil || d.s == nil || d.s.entityRepo == nil {
		return nil, false
	}
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, dataFuncTimeout)
	defer cancel()
	e, err := d.s.entityRepo.GetByID(ctx, uid)
	if err != nil {
		if !errors.Is(err, entity.ErrEntityNotFound) {
			d.fail()
			d.s.logger.Warn("render: ref lookup", "error", err, "schema", schema)
		}
		return nil, false
	}
	if e.Schema == nil || !e.Schema.IsBuiltin || e.Schema.Slug != schema {
		return nil, false
	}
	var a struct {
		Slot string          `json:"slot"`
		Doc  json.RawMessage `json:"page_doc"`
	}
	if json.Unmarshal(e.Attributes, &a) != nil || (slot != "" && a.Slot != slot) {
		return nil, false
	}
	if len(a.Doc) == 0 || string(a.Doc) == "null" {
		return &builder.Doc{V: 2}, true // a block nobody has filled yet
	}
	doc, err := builder.Parse(a.Doc)
	if err != nil {
		d.s.logger.Warn("render: ref doc invalid", "error", err, "entity", uid.String())
		return nil, false
	}
	return doc, true
}
