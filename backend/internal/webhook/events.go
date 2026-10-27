package webhook

import (
	"context"
	"log/slog"
	"time"

	"hatamoto.dev/sdk"
)

// Registrar is the part of the hook engine Register needs.
type Registrar interface {
	AddAction(name string, priority int, fn func(context.Context, any))
}

// Enqueuer queues one event for the matching subscriptions (*Repo).
type Enqueuer interface {
	Enqueue(ctx context.Context, event string, data any, now time.Time) (int, error)
}

// hookPriority runs the listener after plugin listeners, as old core did.
const hookPriority = 1000

// Register subscribes the enqueue listeners to the entity, import and form
// hooks. A listener only inserts rows: it makes no network call, so a slow
// receiver can never make the engine drop or delay an event.
func Register(h Registrar, q Enqueuer, log *slog.Logger) {
	for _, name := range []string{
		sdk.EntityCreated.Name, sdk.EntityUpdated.Name, sdk.EntityPublished.Name, sdk.EntityDeleted.Name,
		sdk.ImportCompleted.Name, sdk.FormSubmitted.Name,
	} {
		h.AddAction(name, hookPriority, func(ctx context.Context, p any) {
			data, ok := mapPayload(name, p)
			if !ok {
				return
			}
			if _, err := q.Enqueue(ctx, name, data, time.Now()); err != nil {
				log.Warn("webhook: enqueue failed", "event", name, "error", err)
			}
		})
	}
}

// The data shapes are part of the receiver contract (docs/WEBHOOKS.md); one
// struct each keeps the JSON key order stable.
type (
	createdData struct {
		EntityID   string `json:"entity_id"`
		EntityName string `json:"entity_name"`
		SchemaSlug string `json:"schema_slug"`
		Locale     string `json:"locale"`
	}
	updatedData struct {
		EntityID   string `json:"entity_id"`
		EntityName string `json:"entity_name"`
		Version    int    `json:"version"`
		Locale     string `json:"locale"`
	}
	publishedData struct {
		EntityID   string `json:"entity_id"`
		EntityName string `json:"entity_name"`
		Locale     string `json:"locale"`
	}
	formData struct {
		FormID       string `json:"form_id"`
		FormSlug     string `json:"form_slug"`
		SubmissionID string `json:"submission_id"`
	}
	importData struct {
		Created int    `json:"created"`
		Failed  int    `json:"failed"`
		Source  string `json:"source"`
		Updated int    `json:"updated"`
	}
)

// mapPayload builds the webhook data of a hook payload. The submitted form
// fields are personal data and are never forwarded: a webhook announces the
// submission, the receiver reads it through the API if it needs it.
func mapPayload(name string, p any) (any, bool) {
	switch ev := p.(type) {
	case sdk.EntityEvent:
		switch name {
		case sdk.EntityCreated.Name, sdk.EntityDeleted.Name:
			return createdData{EntityID: ev.ID, EntityName: ev.Name, SchemaSlug: ev.SchemaSlug, Locale: ev.Locale}, true
		case sdk.EntityUpdated.Name:
			return updatedData{EntityID: ev.ID, EntityName: ev.Name, Version: ev.Version, Locale: ev.Locale}, true
		case sdk.EntityPublished.Name:
			return publishedData{EntityID: ev.ID, EntityName: ev.Name, Locale: ev.Locale}, true
		}
	case sdk.FormSubmittedEvent:
		if name == sdk.FormSubmitted.Name {
			return formData{FormID: ev.FormID, FormSlug: ev.FormSlug, SubmissionID: ev.SubmissionID}, true
		}
	case sdk.ImportEvent:
		if name == sdk.ImportCompleted.Name {
			return importData{Source: ev.Source, Created: ev.Created, Updated: ev.Updated, Failed: ev.Failed}, true
		}
	}
	return nil, false
}
