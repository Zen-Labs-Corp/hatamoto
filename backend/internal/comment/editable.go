package comment

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// HasEditable reports whether the commenter has an approved comment still
// inside its content type's edit window (the ones their device may edit or
// delete). It scans the commenter's recent approved comments and decides each
// window in Go, so the policy has one decoder.
func (s *Service) HasEditable(ctx context.Context, commenterID uuid.UUID) (bool, error) {
	now := s.utcNow()
	rows, err := s.db.Pool().Query(ctx, `SELECT c.created_at, s.settings->'comments'->'policy'
		FROM hatamoto_comments c
		JOIN hatamoto_entities e ON e.id = c.entity_id
		JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		WHERE c.commenter_id = $1 AND c.status = 'approved' AND c.created_at > $2
		ORDER BY c.created_at DESC LIMIT 200`,
		commenterID, now.Add(-time.Duration(PolicyWindowMax)*time.Minute))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var created time.Time
		var raw []byte
		if err := rows.Scan(&created, &raw); err != nil {
			return false, err
		}
		if now.Sub(created) < DecodePolicy(raw).EditWindow() {
			return true, nil
		}
	}
	return false, rows.Err()
}

// EditableComments lists the commenter's approved comments on one entity that
// are still inside its content type's edit window, oldest first, capped like
// ViewerComments.
func (s *Service) EditableComments(ctx context.Context, entityID, commenterID uuid.UUID) ([]ThreadComment, error) {
	// An unknown entity keeps the default window and matches no rows.
	_, pol, err := schemaComments(ctx, s.db.Pool(), entityID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	rows, err := s.db.Pool().Query(ctx, `SELECT id, parent_id, depth, author_name, author_user_id IS NOT NULL,
			body_html, created_at, edited_at
		FROM hatamoto_comments
		WHERE entity_id = $1 AND commenter_id = $2 AND status = 'approved' AND created_at > $3
		ORDER BY created_at, seq LIMIT $4`,
		entityID, commenterID, s.utcNow().Add(-pol.EditWindow()), viewerCommentsLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ThreadComment{}
	for rows.Next() {
		var tc ThreadComment
		if err := rows.Scan(&tc.ID, &tc.ParentID, &tc.Depth, &tc.AuthorName, &tc.IsSiteAuthor,
			&tc.BodyHTML, &tc.CreatedAt, &tc.EditedAt); err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}
