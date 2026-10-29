package server

import (
	"context"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/seo"
)

// seoAdmin is the admin's view of the SEO settings: the store, and the entity
// service that owns the archive title and description of a type.
type seoAdmin struct {
	store    *seo.Store
	entities *entity.Service
}

func (a seoAdmin) Get(ctx context.Context) (seo.Settings, error) { return a.store.Get(ctx) }

func (a seoAdmin) Merge(ctx context.Context, fn func(*seo.Settings)) error {
	return a.store.Merge(ctx, fn)
}

func (a seoAdmin) UpdateSchemaSEO(ctx context.Context, id uuid.UUID, p entity.SchemaSEOPatch) error {
	return a.entities.UpdateSchemaSEOSettings(ctx, id, p)
}
