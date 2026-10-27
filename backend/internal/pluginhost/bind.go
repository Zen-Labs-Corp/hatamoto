package pluginhost

import (
	"context"
	"maps"
	"reflect"
	"time"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/sdk"
)

// filterTimeout bounds filters that run on a request path without a context
// (jsonld.enrich).
const filterTimeout = 2 * time.Second

// BindHostHooks connects core extension points to the hooks engine. Call
// once at boot, after the engine exists.
func BindHostHooks(e *hooks.Engine) {
	entity.SetJSONLDEnricher(func(ent *entity.Entity, baseURL string, doc map[string]any) {
		enrichJSONLD(e, ent, baseURL, doc)
	})
}

func enrichJSONLD(e *hooks.Engine, ent *entity.Entity, baseURL string, doc map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), filterTimeout)
	defer cancel()

	in := sdk.JSONLDDoc{Entity: entityRef(ent), BaseURL: baseURL, Doc: doc}
	out, ok := e.Apply(ctx, sdk.JSONLDEnrich.Name, in).(sdk.JSONLDDoc)
	if !ok || out.Doc == nil {
		return
	}
	// Filters normally mutate Doc in place; if one returned a different map,
	// copy it back so the caller's document reflects the result.
	if !sameMap(out.Doc, doc) {
		for k := range doc {
			delete(doc, k)
		}
		maps.Copy(doc, out.Doc)
	}
}

func sameMap(a, b map[string]any) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

func entityRef(ent *entity.Entity) sdk.EntityRef {
	ref := sdk.EntityRef{
		ID:      ent.ID.String(),
		Slug:    ent.Slug,
		Locale:  ent.Locale,
		Name:    ent.Name,
		Status:  string(ent.Status),
		Version: ent.Version,
	}
	if ent.Schema != nil {
		ref.SchemaSlug = ent.Schema.Slug
	}
	return ref
}
