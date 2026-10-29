package server

import (
	"bytes"
	"context"
	"slices"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/seo"
)

// seoAdmin is the admin's view of the SEO settings: the store, and the entity
// service that owns the archive title and description of a type.
type seoAdmin struct {
	db       *database.DB
	store    *seo.Store
	entities *entity.Service
}

func (a seoAdmin) Get(ctx context.Context) (seo.Settings, error) { return a.store.Get(ctx) }

func (a seoAdmin) Merge(ctx context.Context, fn func(*seo.Settings)) error {
	return a.store.Merge(ctx, fn)
}

// SaveTitles patches the schema rows and merges the templates in one
// transaction. Schema rows are locked in id order and the settings row last, so
// two saves cannot deadlock. The caches are cleared only after the commit.
func (a seoAdmin) SaveTitles(ctx context.Context, patches []admin.SEOTitlePatch, fn func(*seo.Settings)) error {
	patches = slices.Clone(patches)
	slices.SortFunc(patches, func(x, y admin.SEOTitlePatch) int { return bytes.Compare(x.ID[:], y.ID[:]) })
	err := a.db.WithTx(ctx, func(tx pgx.Tx) error {
		for _, p := range patches {
			if err := a.entities.PatchSchemaSEOTx(ctx, tx, p.ID, p.Patch); err != nil {
				return &admin.SEOPatchError{ID: p.ID, Err: err}
			}
		}
		return a.store.MergeTx(ctx, tx, fn)
	})
	if err != nil {
		return err
	}
	a.store.Invalidate()
	if len(patches) > 0 {
		a.entities.SchemaChanged()
	}
	return nil
}
