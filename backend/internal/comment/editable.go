package comment

import (
	"context"

	"github.com/google/uuid"
)

// HasEditable reports whether the commenter has an approved comment still
// inside EditWindow (the ones their device may edit or delete).
func (s *Service) HasEditable(ctx context.Context, commenterID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_comments
		WHERE commenter_id = $1 AND status = 'approved' AND created_at > $2)`,
		commenterID, s.utcNow().Add(-EditWindow)).Scan(&ok)
	return ok, err
}

// EditableComments lists the commenter's approved comments on one entity that
// are still inside EditWindow, oldest first, capped like ViewerComments.
func (s *Service) EditableComments(ctx context.Context, entityID, commenterID uuid.UUID) ([]ThreadComment, error) {
	rows, err := s.db.Pool().Query(ctx, `SELECT id, parent_id, depth, author_name, author_user_id IS NOT NULL,
			body_html, created_at, edited_at
		FROM hatamoto_comments
		WHERE entity_id = $1 AND commenter_id = $2 AND status = 'approved' AND created_at > $3
		ORDER BY created_at, seq LIMIT $4`,
		entityID, commenterID, s.utcNow().Add(-EditWindow), viewerCommentsLimit)
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
