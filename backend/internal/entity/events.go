package entity

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/jsonx"
)

// EventKind names an entity lifecycle event.
type EventKind string

const (
	EventCreated   EventKind = "entity.created"
	EventUpdated   EventKind = "entity.updated"
	EventPublished EventKind = "entity.published"
	EventDeleted   EventKind = "entity.deleted"
	// EventImportCompleted is emitted once per Import batch, in place of the
	// per-entity created/published events.
	EventImportCompleted EventKind = "import.completed"
)

// Event describes a completed entity mutation.
type Event struct {
	Kind       EventKind
	ID         uuid.UUID
	SchemaSlug string
	Slug       string
	Locale     string
	Name       string
	Status     string
	Version    int
	// PrevStatus is the status before the mutation, when known (updates and
	// status changes); empty otherwise. Internal: plugin events do not carry it.
	PrevStatus string
	// Entity is the pre-delete copy (schema, parent, attributes), set for
	// EventDeleted only. Internal: plugin events do not carry it.
	Entity *Entity
	// Import is set for EventImportCompleted only; the entity fields are zero.
	Import *ImportSummary
}

// EventSink receives entity events after a mutation has been persisted.
// Emit is called inline from the service, so implementations must return
// quickly and hand real work to a goroutine; the request context may be
// cancelled as soon as the response is written.
type EventSink interface {
	Emit(ctx context.Context, ev Event)
}

// FanoutSink delivers every event to each non-nil sink, in order.
type FanoutSink []EventSink

// Emit implements EventSink.
func (f FanoutSink) Emit(ctx context.Context, ev Event) {
	for _, s := range f {
		if s != nil {
			s.Emit(ctx, ev)
		}
	}
}

// BeforeSaveInput is the draft handed to the before_save hook. Operation is
// "create", "update" or "restore"; ID is nil for create. The hook may mutate
// Name, Slug and Attributes in place, or return an error to reject the save.
type BeforeSaveInput struct {
	Operation  string
	ID         *uuid.UUID
	SchemaSlug string
	Name       string
	Slug       string
	// Locale is the entity's BCP 47 tag, informational only (never read back).
	// Empty on create or import when the caller did not set one: the entity then
	// takes the site language.
	Locale     string
	Attributes jsontext.Value
}

// BeforeSaveFunc runs before an entity is persisted. A returned error rejects
// the save (wrapped with ErrRejectedByPlugin by the service).
//
// It runs outside the repository transaction: the transaction lives inside
// the repository's Create/Update calls.
type BeforeSaveFunc func(ctx context.Context, in *BeforeSaveInput) error

// ErrRejectedByPlugin is returned (wrapped) when a before_save hook rejects a save.
var ErrRejectedByPlugin = errors.New("save rejected by plugin")

// SetEvents installs the entity event sink. Safe to call before any request
// flows through the service; nil disables emission.
func (s *Service) SetEvents(e EventSink) { s.events = e }

// SetBeforeSave installs the before_save hook; nil disables it.
func (s *Service) SetBeforeSave(f BeforeSaveFunc) { s.beforeSave = f }

func (s *Service) emit(ctx context.Context, kind EventKind, e *Entity, schemaSlug string, opts ...func(*Event)) {
	if s.events == nil || e == nil {
		return
	}
	if schemaSlug == "" && e.Schema != nil {
		schemaSlug = e.Schema.Slug
	}
	ev := Event{
		Kind:       kind,
		ID:         e.ID,
		SchemaSlug: schemaSlug,
		Slug:       e.Slug,
		Locale:     e.Locale,
		Name:       e.Name,
		Status:     string(e.Status),
		Version:    e.Version,
	}
	for _, o := range opts {
		o(&ev)
	}
	s.events.Emit(ctx, ev)
}

func withPrevStatus(st EntityStatus) func(*Event) {
	return func(ev *Event) { ev.PrevStatus = string(st) }
}

// runBeforeSave runs the hook (if any) over the draft. When the hook changed
// the attributes they are re-validated against the schema and re-sanitized,
// so a plugin cannot bypass the write-boundary XSS sanitation. The returned
// attributes are the final ones to persist. A hook-changed blob goes through
// normalizeDoc again against the pre-hook draft: an edit to page_doc is kept
// and validated, an edit to page_sections of a page that has a doc is
// discarded with a warning (the doc is the truth, and refusing would fail the
// save of a hook that cannot know).
func (s *Service) runBeforeSave(ctx context.Context, in *BeforeSaveInput, schema *EntitySchema) (jsontext.Value, error) {
	if s.beforeSave == nil {
		return in.Attributes, nil
	}
	orig := in.Attributes
	if err := s.beforeSave(ctx, in); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRejectedByPlugin, err)
	}
	if bytes.Equal(orig, in.Attributes) {
		return orig, nil
	}
	if err := s.validator.ValidateAttributes(schema.Schema, in.Attributes); err != nil {
		return nil, err
	}
	eligible := EligibleForPageDoc(schema)
	hooked := in.Attributes
	if eligible && hasDocKey(orig) {
		hooked = restoreSections(orig, hooked)
	}
	clean, err := sanitizeAttributes(hooked)
	if err != nil {
		return nil, err
	}
	// The baseline is the pre-hook draft (already normalised), not the stored
	// page: a changed page_doc is validated and kept.
	return normalizeDocRaw(docWrite, orig, clean, eligible)
}

// restoreSections puts the pre-hook page_sections (or none) back into a
// hook-edited blob, warning when the hook had changed it.
func restoreSections(orig, hooked jsontext.Value) jsontext.Value {
	var before, after map[string]jsontext.Value
	if jsonx.Unmarshal(orig, &before, jsonx.StoredLoose) != nil || jsonx.Unmarshal(hooked, &after, jsonx.StoredLoose) != nil {
		return hooked
	}
	prev, had := before[keySections]
	cur, has := after[keySections]
	if had == has && (!had || sameRawJSON(prev, cur)) {
		return hooked
	}
	slog.Warn("before-save hook change to page_sections discarded; change page_doc instead")
	if had {
		after[keySections] = prev
	} else {
		delete(after, keySections)
	}
	b, err := jsonx.Marshal(after)
	if err != nil {
		return hooked
	}
	return b
}

func sameRawJSON(a, b jsontext.Value) bool {
	var x, y any
	return jsonx.Unmarshal(a, &x, jsonx.StoredLoose) == nil && jsonx.Unmarshal(b, &y, jsonx.StoredLoose) == nil && sameJSON(x, y)
}
