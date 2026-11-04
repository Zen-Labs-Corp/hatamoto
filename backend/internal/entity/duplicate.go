package entity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/google/uuid"
)

// ErrDuplicateUnsupported is returned for a source that is never duplicated:
// people and reusable blocks.
var ErrDuplicateUnsupported = fmt.Errorf("%w: this kind of entry cannot be duplicated", ErrValidationFailed)

// maxDuplicateSlugs bounds the "-copy-N" suffixes tried before giving up.
const maxDuplicateSlugs = 50

// Duplicator creates same-locale draft copies of entities.
type Duplicator struct {
	svc   *Service
	terms CopyTerms
}

// NewDuplicator returns a Duplicator. terms may be nil: copies then carry no
// terms.
func NewDuplicator(svc *Service, terms CopyTerms) *Duplicator {
	return &Duplicator{svc: svc, terms: terms}
}

// CanDuplicate reports whether an entity of schema sc may be duplicated:
// people and reusable blocks may not.
func CanDuplicate(sc *EntitySchema) bool {
	return sc == nil || (sc.Slug != personSchemaSlug && !IsReuseSchema(sc))
}

// Duplicate creates a draft copy of srcID in the source's locale and parent,
// named "Copy of <name>" with the slug "<slug>-copy" ("-copy-2", ...). It
// keeps the attributes (without canonical_url), metadata, byline and terms,
// and never the status, dates or translation group. A nil userID acts as the
// system. A failed terms copy leaves the copy in place.
func (d *Duplicator) Duplicate(ctx context.Context, userID, srcID uuid.UUID) (*Entity, error) {
	src, err := d.svc.GetEntity(ctx, srcID)
	if err != nil {
		return nil, err
	}
	if !CanDuplicate(src.Schema) {
		return nil, ErrDuplicateUnsupported
	}
	attrs, err := withoutCanonical(src.Attributes)
	if err != nil {
		return nil, err
	}
	in := &CreateEntityInput{
		SchemaID:       src.SchemaID,
		Name:           "Copy of " + src.Name,
		Locale:         src.Locale,
		ParentID:       src.ParentID,
		Attributes:     attrs,
		Metadata:       src.Metadata,
		AuthorID:       src.AuthorID,
		BylinePersonID: src.BylinePersonID,
		BylineOrg:      src.BylineOrg,
	}
	var cp *Entity
	for n := 1; n <= maxDuplicateSlugs; n++ {
		in.Slug = src.Slug + "-copy"
		if n > 1 {
			in.Slug += "-" + strconv.Itoa(n)
		}
		cp, err = d.svc.CreateEntity(ctx, actor(userID), in)
		if !errors.Is(err, ErrSlugAlreadyExists) {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	d.copyTerms(ctx, src, cp)
	return cp, nil
}

func (d *Duplicator) copyTerms(ctx context.Context, src, cp *Entity) {
	if d.terms == nil {
		return
	}
	ids, err := d.terms.MapTerms(ctx, src.ID, src.Locale)
	if err == nil && len(ids) > 0 {
		err = d.terms.ReplaceEntityTerms(ctx, cp.ID, ids)
	}
	if err != nil {
		d.svc.logger.Warn("duplicate: terms not copied", slog.String("entity_id", cp.ID.String()), slog.String("error", err.Error()))
	}
}
