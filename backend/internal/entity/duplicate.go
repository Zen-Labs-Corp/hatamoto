package entity

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/jsonx"
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
	name := "Copy of " + src.Name
	if attrs, err = retitle(attrs, src.Name, name); err != nil {
		return nil, err
	}
	meta, err := withoutImportKey(src.Metadata)
	if err != nil {
		return nil, err
	}
	locale := src.Locale
	if locale == LocaleShared {
		locale = "" // CreateEntity refuses und; the schema default restores it
	}
	base := CreateEntityInput{
		SchemaID:       src.SchemaID,
		Name:           name,
		Locale:         locale,
		ParentID:       src.ParentID,
		Attributes:     attrs,
		Metadata:       meta,
		AuthorID:       src.AuthorID,
		BylinePersonID: src.BylinePersonID,
		BylineOrg:      src.BylineOrg,
	}
	var cp *Entity
	for n := 1; n <= maxDuplicateSlugs; n++ {
		in := base // CreateEntity writes hook results back into its input
		in.Slug = src.Slug + "-copy"
		if n > 1 {
			in.Slug += "-" + strconv.Itoa(n)
		}
		cp, err = d.svc.CreateEntity(ctx, actor(userID), &in)
		if !errors.Is(err, ErrSlugAlreadyExists) {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	d.placeAfter(ctx, src, cp)
	d.copyTerms(ctx, src, cp)
	return cp, nil
}

// placeAfter puts the copy right after its source when the source's siblings
// are manually ordered; otherwise the copy keeps the default place.
func (d *Duplicator) placeAfter(ctx context.Context, src, cp *Entity) {
	os, ok := d.svc.entityRepo.(OrderStore)
	if !ok || src.SortOrder < 1 {
		return
	}
	if _, _, err := os.SetPosition(ctx, cp.ID, src.SortOrder+1); err != nil {
		d.svc.logger.Warn("duplicate: copy not placed after the source", slog.String("entity_id", cp.ID.String()), slog.String("error", err.Error()))
	}
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

// withoutImportKey returns metadata without the importer's import_key: a
// copy is not the imported item, and import_key is unique across entities.
func withoutImportKey(meta jsontext.Value) (jsontext.Value, error) {
	if len(meta) == 0 {
		return meta, nil
	}
	var m map[string]jsontext.Value
	if err := jsonx.Unmarshal(meta, &m, jsonx.StoredLoose); err != nil {
		return nil, fmt.Errorf("read metadata: %w", err)
	}
	if _, ok := m["import_key"]; !ok {
		return meta, nil
	}
	delete(m, "import_key")
	out, err := jsonx.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("write metadata: %w", err)
	}
	return out, nil
}

// retitle sets attributes.title to name when it mirrors the source's name, as
// the admin keeps it for types that declare a title: public listings prefer it
// over the name.
func retitle(attrs jsontext.Value, from, name string) (jsontext.Value, error) {
	var m map[string]jsontext.Value
	if err := jsonx.Unmarshal(attrs, &m, jsonx.StoredLoose); err != nil {
		return nil, fmt.Errorf("read attributes: %w", err)
	}
	var title string
	if jsonx.Unmarshal(m["title"], &title, jsonx.StoredLoose) != nil || title != from {
		return attrs, nil
	}
	m["title"], _ = jsonx.Marshal(name)
	out, err := jsonx.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("write attributes: %w", err)
	}
	return out, nil
}
