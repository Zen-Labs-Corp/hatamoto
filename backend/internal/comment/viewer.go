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

// ReplyParent reports whether a reply to parentID is allowed on the entity:
// the parent exists there and is approved (the only status the thread shows,
// so a reply under anything else would never be visible).
func (s *Service) ReplyParent(ctx context.Context, entityID, parentID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_comments
		WHERE id = $1 AND entity_id = $2 AND status = 'approved')`, parentID, entityID).Scan(&ok)
	return ok, err
}

// HasNonPublic reports whether the commenter has a draft, pending, spam or dead
// comment (what only their own device may see; a dead one is shown to its author).
func (s *Service) HasNonPublic(ctx context.Context, commenterID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_comments
		WHERE commenter_id = $1 AND status IN ('draft','pending','spam','dead'))`, commenterID).Scan(&ok)
	return ok, err
}

// ViewerComments lists the commenter's own draft, pending, spam and dead comments
// on one entity, oldest first, as ThreadComment (public fields only, plus Status
// and Rank), capped at 50.
func (s *Service) ViewerComments(ctx context.Context, entityID, commenterID uuid.UUID) ([]ThreadComment, error) {
	rows, err := s.db.Pool().Query(ctx, `SELECT id, parent_id, depth, author_name, author_user_id IS NOT NULL,
			body_html, created_at, edited_at, status, rank
		FROM hatamoto_comments
		WHERE entity_id = $1 AND commenter_id = $2 AND status IN ('draft','pending','spam','dead')
		ORDER BY created_at, seq LIMIT $3`, entityID, commenterID, viewerCommentsLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ThreadComment{}
	for rows.Next() {
		var tc ThreadComment
		var status string
		if err := rows.Scan(&tc.ID, &tc.ParentID, &tc.Depth, &tc.AuthorName, &tc.IsSiteAuthor,
			&tc.BodyHTML, &tc.CreatedAt, &tc.EditedAt, &status, &tc.Rank); err != nil {
			return nil, err
		}
		tc.Status = Status(status)
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
