package worker

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/database"
)

// PublishWorker promotes scheduled entities to published once their
// published_at falls due.
//
// Why a status rather than a date filter: the public repository
// (internal/entity/public_repository.go) selects on status = 'published'
// across eight query sites with no date guard. Adding
// "AND published_at <= NOW()" to each would work until someone adds a
// ninth and leaks a post early. A distinct 'scheduled' status is excluded
// by every one of those queries for free, and this worker is the single
// place the transition happens.
//
// The UPDATE deliberately does not touch published_at: the scheduled instant
// is the publication date. entity.UpdateStatus only stamps time.Now() when
// published_at is NULL or still in the future (a post published early by
// hand), so it keeps a past date, but the worker stays a separate, explicit
// path.
type PublishWorker struct {
	db     *database.DB
	logger *slog.Logger
	events PublishEventSink
}

// PublishEventSink is told about each entity the worker promotes so the
// plugin host can fire entity.published. Defined here (primitives only) to
// keep this package free of entity/pluginhost imports.
type PublishEventSink interface {
	EntityPublished(ctx context.Context, id uuid.UUID, schemaSlug, slug, name string, version int)
}

// SetEvents installs the sink; nil disables notification.
func (w *PublishWorker) SetEvents(s PublishEventSink) { w.events = s }

// NewPublishWorker constructs the worker.
func NewPublishWorker(db *database.DB, logger *slog.Logger) *PublishWorker {
	return &PublishWorker{db: db, logger: logger}
}

// PublishedEntity identifies one row this worker promoted.
type PublishedEntity struct {
	ID         uuid.UUID
	Slug       string
	Name       string
	SchemaSlug string
	Version    int
}

// Run executes one tick: promote every scheduled entity whose published_at
// has passed. Returns the rows promoted.
//
// Each promoted entity goes through the events sink, which clears the page
// cache (the server's sink does so synchronously) so the page and listings
// are live as soon as Run returns, whatever HATAMOTO_PAGE_CACHE_TTL is.
func (w *PublishWorker) Run(ctx context.Context) ([]PublishedEntity, error) {
	adminCtx := database.WithSuperadmin(ctx)

	conn, err := w.db.GetConn(adminCtx)
	if err != nil {
		return nil, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	rows, err := conn.Query(adminCtx, `
		UPDATE hatamoto_entities
		   SET status = 'published',
		       updated_at = NOW()
		 WHERE status = 'scheduled'
		   AND published_at <= NOW()
		RETURNING id, slug, name, version,
		          COALESCE((SELECT s.slug FROM hatamoto_entity_schemas s
		                     WHERE s.id = hatamoto_entities.schema_id), '')
	`)
	if err != nil {
		return nil, fmt.Errorf("promote scheduled entities: %w", err)
	}
	defer rows.Close()

	var published []PublishedEntity
	for rows.Next() {
		var e PublishedEntity
		if err := rows.Scan(&e.ID, &e.Slug, &e.Name, &e.Version, &e.SchemaSlug); err != nil {
			return nil, fmt.Errorf("scan promoted entity: %w", err)
		}
		published = append(published, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate promoted entities: %w", err)
	}

	for _, e := range published {
		w.logger.Info("scheduled entity published",
			slog.String("slug", e.Slug),
			slog.String("name", e.Name),
			slog.String("entity_id", e.ID.String()),
		)
		if w.events != nil {
			w.events.EntityPublished(ctx, e.ID, e.SchemaSlug, e.Slug, e.Name, e.Version)
		}
	}
	return published, nil
}
