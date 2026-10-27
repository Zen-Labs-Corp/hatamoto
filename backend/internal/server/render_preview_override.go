package server

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

// previewOverride carries unsaved attributes for one entity through the public
// loaders, so a draft preview is built by exactly the code that builds the
// public page: the loaders substitute the attributes right after loading the
// entity and before meta, JSON-LD and sections are derived.
type previewOverride struct {
	entityID uuid.UUID
	attrs    json.RawMessage
}

type previewOverrideKey struct{}

func withPreviewAttributes(ctx context.Context, entityID uuid.UUID, attrs json.RawMessage) context.Context {
	return context.WithValue(ctx, previewOverrideKey{}, previewOverride{entityID: entityID, attrs: attrs})
}

// applyPreviewAttributes replaces *attrs with the preview override when the
// context carries one for entityID. Any other entity is left untouched, so a
// preview can never alter, nor expose, a different entity's data.
func applyPreviewAttributes(ctx context.Context, entityID uuid.UUID, attrs *json.RawMessage) {
	o, ok := ctx.Value(previewOverrideKey{}).(previewOverride)
	if !ok || o.entityID != entityID || len(o.attrs) == 0 {
		return
	}
	*attrs = o.attrs
}
