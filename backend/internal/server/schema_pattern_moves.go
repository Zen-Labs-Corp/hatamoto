package server

import (
	"context"
	"encoding/json"
	"log/slog"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/redirect"
)

// patternMovePage is how many entities one query reads when a schema's URL
// pattern changes.
const patternMovePage = 200

// onSchemaPatternChanged runs after a schema update changed its URL pattern or
// overrides. Every published entity of the schema that now lives at another
// path keeps a 301 from the old one. Redirects that already exist stay exactly
// as they are (an owner's rule for /author/x wins over the automatic one), and
// a move that would loop is skipped. Failures are logged: the save itself
// already succeeded.
func (s *Server) onSchemaPatternChanged(ctx context.Context, before, after *entity.EntitySchema) {
	if s.redirectService == nil || s.db == nil {
		return
	}
	log := s.logger.With(slog.String("schema", after.Slug))
	oldSnap, err1 := entity.BuildURLSnapshot([]*entity.EntitySchema{before}, s.logger)
	newSnap, err2 := entity.BuildURLSnapshot([]*entity.EntitySchema{after}, s.logger)
	if err1 != nil || err2 != nil {
		log.Warn("schema pattern move: url snapshot", slog.Any("old", err1), slog.Any("new", err2))
		return
	}
	repo := entity.NewPostgresPublicRepository(s.db)
	published := entity.StatusPublished
	filter := &entity.ListEntitiesFilter{SchemaID: &after.ID, Status: &published}
	var moves []redirect.PathMove
	for page := 1; ; page++ {
		res, err := s.entityService.ListEntitiesWithOffset(ctx, filter, &entity.OffsetPage{Page: page, Limit: patternMovePage})
		if err != nil {
			log.Warn("schema pattern move: list entities", slog.String("error", err.Error()))
			return
		}
		for _, e := range res.Items {
			if after.Slug == entity.PersonSchemaSlug && !publicPerson(e) {
				continue // no page at either path: a redirect would only reveal the slug
			}
			e.Schema = after
			from := locateEntity(ctx, oldSnap, repo, e).Path
			to := locateEntity(ctx, newSnap, repo, e).Path
			if from != to {
				moves = append(moves, redirect.PathMove{From: from, To: to})
			}
		}
		if page >= res.TotalPages || len(res.Items) == 0 {
			break
		}
	}
	if len(moves) == 0 {
		return
	}
	n, err := s.redirectService.KeepEntityPaths(ctx, moves)
	if err != nil {
		log.Warn("schema pattern move: redirects", slog.String("error", err.Error()))
		return
	}
	log.Info("schema pattern changed: old paths redirect to the new ones", slog.Int("moves", len(moves)), slog.Int("redirects_written", n))
	s.invalidateRendered(ctx)
}

// publicPerson reports whether a person opted in to a public page.
func publicPerson(e *entity.Entity) bool {
	var attrs struct {
		Public bool `json:"public"`
	}
	_ = json.Unmarshal(e.Attributes, &attrs)
	return attrs.Public
}
