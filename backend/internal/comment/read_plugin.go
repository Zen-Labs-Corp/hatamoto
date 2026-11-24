package comment

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/entity"
)

// AuthorPageSize is the page size of ByAuthor.
const AuthorPageSize = 50

// DeadScope names the plugin and content type whose dead comments ByAuthor may
// return. The zero value returns none. The caller sets it only when include_dead
// passed the comments.moderate gate; the SQL term below mirrors the plugin
// reader's revealDead (owner = the type's origin plugin AND its slug), so every
// dead row returned is one the caller may see in full.
type DeadScope struct{ Plugin, Schema string }

// byAuthorSQL: the commenter's approved comments on public entities (plus the
// dead ones of the DeadScope type), newest first. The visibility terms mirror
// the plugin reader's visible(): published, entity.HiddenPersonFor with the
// visible locales ($4), and a resolved mode that is not hidden (ResolveMode:
// entity mode, else type mode, else open; only a valid value counts). The site
// switch is checked in Go.
var byAuthorSQL = `
SELECT c.id, c.parent_id, c.depth, c.author_name, c.author_user_id IS NOT NULL,
       c.body_html, c.created_at, c.edited_at, c.status, c.rank, c.commenter_id
FROM hatamoto_comments c
JOIN hatamoto_entities e ON e.id = c.entity_id
JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
LEFT JOIN hatamoto_comment_entities ce ON ce.entity_id = e.id
WHERE c.commenter_id = $1
  AND (c.status = 'approved'
       OR (c.status = 'dead' AND $5::text <> '' AND COALESCE(s.origin_plugin, '') = $5 AND s.slug = $6))
  AND e.status = 'published' AND ` + entity.HiddenPersonFor("$4") + `
  AND CASE WHEN ce.mode IN ('open','closed','hidden') THEN ce.mode
           WHEN s.settings->'comments'->>'mode' IN ('open','closed','hidden') THEN s.settings->'comments'->>'mode'
           ELSE 'open' END <> 'hidden'
ORDER BY c.created_at DESC, c.seq DESC
LIMIT $2 OFFSET $3`

// ByAuthor returns one page (AuthorPageSize) of the commenter's approved
// comments on public entities, plus the dead ones on dead's content type,
// newest first, in one query. The visible locales come from ctx
// (entity.VisibleLocales). Comments off: an empty page. A dead row keeps its
// body: masking is the caller's job, as for Thread.
func (s *Service) ByAuthor(ctx context.Context, commenterID uuid.UUID, page int, dead DeadScope) (ThreadPage, error) {
	if page < 1 {
		page = 1
	}
	out := ThreadPage{Page: page, Pages: page, Comments: []ThreadComment{}}
	set, err := s.Settings(ctx)
	if err != nil || !set.Enabled {
		return out, err
	}
	rows, err := s.db.Pool().Query(ctx, byAuthorSQL, commenterID, AuthorPageSize+1, (page-1)*AuthorPageSize,
		entity.VisibleLocales(ctx), dead.Plugin, dead.Schema)
	if err != nil {
		return ThreadPage{}, err
	}
	cs, err := scanPublic(rows)
	if err != nil {
		return ThreadPage{}, err
	}
	if len(cs) > AuthorPageSize {
		cs, out.HasMore = cs[:AuthorPageSize], true
		out.Pages++
	}
	out.Comments = cs
	return out, nil
}

// scanPublic reads the 11 public columns of a comment row (the order of
// byAuthorSQL) and closes rows. The result is never nil.
func scanPublic(rows pgx.Rows) ([]ThreadComment, error) {
	defer rows.Close()
	out := []ThreadComment{}
	for rows.Next() {
		var tc ThreadComment
		var status string
		if err := rows.Scan(&tc.ID, &tc.ParentID, &tc.Depth, &tc.AuthorName, &tc.IsSiteAuthor,
			&tc.BodyHTML, &tc.CreatedAt, &tc.EditedAt, &status, &tc.Rank, &tc.CommenterID); err != nil {
			return nil, err
		}
		tc.Status = Status(status)
		out = append(out, tc)
	}
	return out, rows.Err()
}

// CommenterStamp is a commenter id with its creation time (host-only; for key resolution).
type CommenterStamp struct {
	ID        uuid.UUID
	CreatedAt time.Time
}

// CommentersSince lists commenters created after since (zero: all), oldest first.
func (s *Service) CommentersSince(ctx context.Context, since time.Time) ([]CommenterStamp, error) {
	rows, err := s.db.Pool().Query(ctx, `SELECT id, created_at FROM hatamoto_commenters WHERE created_at > $1 ORDER BY created_at`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CommenterStamp
	for rows.Next() {
		var c CommenterStamp
		if err := rows.Scan(&c.ID, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
