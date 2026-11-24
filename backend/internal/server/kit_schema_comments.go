package server

import (
	"context"
	"errors"
	"fmt"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/kit"
)

// kitSchemaComments gives a kit the comment settings seam of Settings > Comments >
// Content types: it finds a type by slug and writes through
// commentsAdmin.SetSchemaComments, which validates.
type kitSchemaComments struct{ a commentsAdmin }

var _ kit.SchemaComments = kitSchemaComments{}

// KitSchemaComments is the seam for callers outside the server (the CLI).
func KitSchemaComments(svc *comment.Service, db *database.DB, ents *entity.Service) kit.SchemaComments {
	return kitSchemaComments{commentsAdmin{svc: svc, db: db, entities: ents}}
}

// kitSchemaComments returns the seam, nil when the server has no comment service.
func (s *Server) kitSchemaComments() kit.SchemaComments {
	if s.comments == nil || s.db == nil || s.entityService == nil {
		return nil
	}
	return KitSchemaComments(s.comments, s.db, s.entityService)
}

func (k kitSchemaComments) SchemaComments(ctx context.Context, slug string) (kit.SchemaCommentState, error) {
	sch, err := k.a.entities.GetSchemaBySlug(ctx, slug)
	if err != nil {
		return kit.SchemaCommentState{}, err
	}
	cfg, hasPolicy, err := schemaCommentConfig(sch)
	if err != nil {
		return kit.SchemaCommentState{}, err
	}
	return kit.SchemaCommentState{
		ID:        cfg.ID,
		Builtin:   sch.IsBuiltin && !k.a.Supports("schema-mode-builtin"),
		Order:     cfg.Order,
		Policy:    comment.Policy{Enabled: cfg.Policy.Enabled, MaxDepth: cfg.Policy.MaxDepth, EditWindowMinutes: cfg.Policy.EditWindowMinutes, SlowModeSeconds: cfg.Policy.SlowModeSeconds},
		HasPolicy: hasPolicy,
	}, nil
}

func (k kitSchemaComments) SetSchemaComments(ctx context.Context, slug, order string, p comment.Policy) error {
	sch, err := k.a.entities.GetSchemaBySlug(ctx, slug)
	if err != nil {
		return err
	}
	err = k.a.SetSchemaComments(ctx, sch.ID.String(), order, admin.CommentPolicy{
		Enabled: p.Enabled, MaxDepth: p.MaxDepth, EditWindowMinutes: p.EditWindowMinutes, SlowModeSeconds: p.SlowModeSeconds,
	})
	var fe *admin.CommentFieldError
	switch {
	case errors.As(err, &fe):
		return fmt.Errorf("%s %s", fe.Field, fe.Message)
	case errors.Is(err, admin.ErrCommentsUnsupported):
		return fmt.Errorf("content type %s: %w", slug, err)
	}
	return err
}
