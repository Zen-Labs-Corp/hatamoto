package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"reflect"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/sdk"
)

// EntitySink turns entity lifecycle events into entity.* hook actions, which
// run asynchronously off the request path (so a slow plugin can never block a
// save). It implements entity.EventSink and
// worker.PublishEventSink.
type EntitySink struct {
	engine *hooks.Engine
	log    *slog.Logger
	locale func(context.Context, uuid.UUID) string
}

// NewEntitySink builds the sink on engine.
func NewEntitySink(engine *hooks.Engine, log *slog.Logger) *EntitySink {
	if log == nil {
		log = slog.Default()
	}
	return &EntitySink{engine: engine, log: log}
}

// Emit implements entity.EventSink.
func (s *EntitySink) Emit(ctx context.Context, ev entity.Event) {
	if ev.Kind == entity.EventImportCompleted && ev.Import != nil {
		s.engine.Fire(ctx, string(ev.Kind), sdk.ImportEvent{
			Source: ev.Import.Source, Created: ev.Import.Created, Updated: ev.Import.Updated, Failed: ev.Import.Failed,
		})
		return
	}
	s.engine.Fire(ctx, string(ev.Kind), sdk.EntityEvent{
		ID:         ev.ID.String(),
		SchemaSlug: ev.SchemaSlug,
		Slug:       ev.Slug,
		Locale:     ev.Locale,
		Name:       ev.Name,
		Status:     ev.Status,
		Version:    ev.Version,
	})
}

// SetLocaleLookup wires the entity locale reader. The worker interface
// (internal/worker, not owned by this change) carries no locale, so a
// scheduled publish looks it up here; an unset or failing lookup leaves the
// locale of entity.published empty.
func (s *EntitySink) SetLocaleLookup(f func(context.Context, uuid.UUID) string) { s.locale = f }

// EntityPublished implements worker.PublishEventSink for scheduled publishes.
func (s *EntitySink) EntityPublished(ctx context.Context, id uuid.UUID, schemaSlug, slug, name string, version int) {
	loc := ""
	if s.locale != nil {
		loc = s.locale(ctx, id)
	}
	s.Emit(ctx, entity.Event{
		Kind: entity.EventPublished, ID: id, SchemaSlug: schemaSlug, Locale: loc,
		Slug: slug, Name: name, Status: string(entity.StatusPublished), Version: version,
	})
}

// BeforeSave adapts the engine's strict entity.before_save filter chain to
// entity.BeforeSaveFunc.
func BeforeSave(engine *hooks.Engine) entity.BeforeSaveFunc {
	return func(ctx context.Context, in *entity.BeforeSaveInput) error {
		// The hook sees page_sections derived from page_doc, never the frozen
		// mirror; whatever it leaves unchanged there is put back as stored.
		stored := in.Attributes
		shown := entity.DerivedAttributes(stored)
		draft := sdk.EntityDraft{
			Operation:  in.Operation,
			SchemaSlug: in.SchemaSlug,
			Name:       in.Name,
			Slug:       in.Slug,
			Locale:     in.Locale,
			Attributes: shown,
		}
		if in.ID != nil {
			draft.ID = in.ID.String()
		}
		out, err := engine.ApplyStrict(ctx, sdk.EntityBeforeSave.Name, draft)
		if err != nil {
			return err
		}
		if d, ok := out.(sdk.EntityDraft); ok {
			in.Name, in.Slug, in.Attributes = d.Name, d.Slug, d.Attributes
			if bytes.Equal(d.Attributes, shown) {
				in.Attributes = stored
			} else {
				in.Attributes = keepStoredSections(stored, shown, d.Attributes)
			}
		}
		return nil
	}
}

// keepStoredSections undoes the derived page_sections the hook was shown when
// it did not edit them: if out's page_sections equals shown's, the stored
// value (or absence) comes back, so only a real hook edit reaches the seam.
func keepStoredSections(stored, shown, out json.RawMessage) json.RawMessage {
	var s, o map[string]json.RawMessage
	var shownM map[string]json.RawMessage
	if json.Unmarshal(stored, &s) != nil || json.Unmarshal(out, &o) != nil || json.Unmarshal(shown, &shownM) != nil {
		return out
	}
	if !jsonEqual(shownM["page_sections"], o["page_sections"]) {
		return out
	}
	if v, ok := s["page_sections"]; ok {
		o["page_sections"] = v
	} else {
		delete(o, "page_sections")
	}
	b, err := json.Marshal(o)
	if err != nil {
		return out
	}
	return b
}

func jsonEqual(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}
