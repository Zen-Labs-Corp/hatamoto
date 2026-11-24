package server

import (
	"context"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/entity"
)

// commentCascade lets the entity service announce the comments an entity
// delete removes with it (the table cascades silently).
type commentCascade struct{ svc *comment.Service }

func (c commentCascade) CollectForEntity(ctx context.Context, q entity.Querier, id uuid.UUID) (func(context.Context), error) {
	return c.svc.CollectForEntity(ctx, q, id)
}

// wireCommentCascade registers the cascade once both services exist.
func (s *Server) wireCommentCascade() {
	if s.entityService == nil || s.comments == nil || s.db == nil {
		return
	}
	s.entityService.SetDeleteCascade(commentCascade{s.comments}, s.db.Pool())
}
