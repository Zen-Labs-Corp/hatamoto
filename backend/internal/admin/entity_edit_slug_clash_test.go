package admin

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

// clashEntities refuses every create the way the service does for a slug used
// on the other side of the shared / language-specific split.
type clashEntities struct{ EntityStore }

func (clashEntities) CreateEntity(context.Context, *uuid.UUID, *entity.CreateEntityInput) (*entity.Entity, error) {
	return nil, fmt.Errorf("%w: another entry of this type already uses this slug, in all languages or in a specific language", entity.ErrSlugAlreadyExists)
}

// The service wraps ErrSlugAlreadyExists for a cross-language clash; the form
// must still show it on the slug field with a 422, not fall through to a 500.
func TestCreateCrossLocaleSlugClashShowsSlugError(t *testing.T) {
	t.Parallel()
	c, _ := neutralRig(t, "en", "fr")
	c.a.deps.Content.Entities = clashEntities{c.a.deps.Content.Entities}
	empty := ""
	rec := c.post("/admin/entities", recipeForm(&empty))
	if rec.Code != 422 {
		t.Fatalf("create: %d, want 422\n%s", rec.Code, rec.Body.String())
	}
	has(t, rec.Body.String(), "That slug is already used by another entity")
}
