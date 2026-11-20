package comment

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// viewerCommentsLimit caps ViewerComments.
const viewerCommentsLimit = 50

// Get returns one comment by id (any status), or ErrNotFound. Used by the public
// edit/delete path to check ownership and the edit window; the caller never shows
// body_raw publicly.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Comment, error) {
	c, err := scanComment(s.db.Pool().QueryRow(ctx, `SELECT `+commentColumns+` FROM hatamoto_comments WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Comment{}, ErrNotFound
	}
	return c, err
}

// HasNonPublic reports whether the commenter has a draft, pending or spam comment
// (what only their own device may see).
func (s *Service) HasNonPublic(ctx context.Context, commenterID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_comments
		WHERE commenter_id = $1 AND status IN ('draft','pending','spam'))`, commenterID).Scan(&ok)
	return ok, err
}

// ViewerComments lists the commenter's own draft, pending and spam comments on one
// entity, oldest first, as ThreadComment (public fields only), capped at 50.
func (s *Service) ViewerComments(ctx context.Context, entityID, commenterID uuid.UUID) ([]ThreadComment, error) {
	rows, err := s.db.Pool().Query(ctx, `SELECT id, parent_id, depth, author_name, author_user_id IS NOT NULL,
			body_html, created_at, edited_at
		FROM hatamoto_comments
		WHERE entity_id = $1 AND commenter_id = $2 AND status IN ('draft','pending','spam')
		ORDER BY created_at, seq LIMIT $3`, entityID, commenterID, viewerCommentsLimit)
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

// DuplicateSince counts comments with this body hash created at or after since
// (any status except trash). An empty hash counts 0 without a query.
func (s *Service) DuplicateSince(ctx context.Context, bodyHash string, since time.Time) (int, error) {
	if bodyHash == "" {
		return 0, nil
	}
	var n int
	err := s.db.Pool().QueryRow(ctx, `SELECT count(*)::int FROM hatamoto_comments
		WHERE body_hash = $1 AND created_at >= $2 AND status <> 'trash'`, bodyHash, since.UTC()).Scan(&n)
	return n, err
}
