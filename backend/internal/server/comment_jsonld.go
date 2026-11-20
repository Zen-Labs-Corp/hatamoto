package server

import (
	"context"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/entity"
)

// commentCounter is the slice of *comment.Service the JSON-LD count needs.
type commentCounter interface {
	EntityMode(context.Context, uuid.UUID) (comment.Mode, error)
	Counts(context.Context, []uuid.UUID) (map[uuid.UUID]comment.EntityCounts, error)
}

// withCommentCount adds commentCount (approved comments only) to the entity's
// JSON-LD node. It leaves doc unchanged without a comment service, for an
// entity whose comments are off or hidden, with no approved comment, or when a
// read fails. No Comment nodes and no author or url fields are added. The
// observer drops the entity's pages when the count changes.
func (s *Server) withCommentCount(ctx context.Context, ent *entity.Entity, doc map[string]any) map[string]any {
	if s.comments == nil || ent == nil {
		return doc
	}
	return addCommentCount(ctx, s.comments, ent.ID, doc)
}

func addCommentCount(ctx context.Context, cc commentCounter, id uuid.UUID, doc map[string]any) map[string]any {
	if doc == nil {
		return doc
	}
	mode, err := cc.EntityMode(ctx, id)
	if err != nil || (mode != comment.ModeOpen && mode != comment.ModeClosed) {
		return doc
	}
	counts, err := cc.Counts(ctx, []uuid.UUID{id})
	if err != nil || counts[id].Approved <= 0 {
		return doc
	}
	node := doc
	if graph, ok := doc["@graph"].([]map[string]any); ok {
		node = nil
		for _, n := range graph {
			switch n["@type"] {
			case "WebPage", "BreadcrumbList", "FAQPage":
			default:
				node = n
			}
			if node != nil {
				break
			}
		}
		if node == nil {
			return doc
		}
	}
	node["commentCount"] = counts[id].Approved
	return doc
}
