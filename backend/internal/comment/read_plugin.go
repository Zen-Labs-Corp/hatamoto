package comment

import (
	"context"
	"errors"
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

// SubtreePageSize caps the items of one Subtree page.
const SubtreePageSize = 200

// SubtreePage is one comment's ancestors (root first) and one page of the
// comment plus its replies in path order. EntityID is host-only: the caller
// checks the entity is public before returning anything.
type SubtreePage struct {
	EntityID  uuid.UUID
	Ancestors []ThreadComment
	Comments  []ThreadComment
	Page      int
	HasMore   bool
}

// subtreeCols are the public columns, in scanPublic's order.
const subtreeCols = `c.id, c.parent_id, c.depth, c.author_name, c.author_user_id IS NOT NULL,
       c.body_html, c.created_at, c.edited_at, c.status, c.rank, c.commenter_id`

// subtreeItemsSQL: the comment ($2 = its path) and its shown descendants,
// minus every row under a hidden one inside the range (the thread rule: a reply
// whose parent is neither approved nor dead is dropped with its subtree), so
// LIMIT and has_more are exact.
const subtreeItemsSQL = `
WITH bad AS (
  SELECT path FROM hatamoto_comments
  WHERE entity_id = $1 AND path > $2 AND path < $2 || '/' AND status NOT IN ('approved', 'dead'))
SELECT ` + subtreeCols + `
FROM hatamoto_comments c
WHERE c.entity_id = $1 AND c.status IN ('approved', 'dead')
  AND c.path >= $2 AND c.path < $2 || '/'
  AND NOT EXISTS (SELECT 1 FROM bad b WHERE c.path > b.path AND c.path < b.path || '/')
ORDER BY c.path
LIMIT $3 OFFSET $4`

// Subtree reads one comment with its ancestors and replies. Comments off, an
// unknown id, or a comment or ancestor that is neither approved nor dead is
// ErrNotFound (one error for all, so callers cannot tell them apart). Dead
// counts as shown (thread rule): a dead comment or ancestor is returned with its
// body; hiding it is the caller's job, as for Thread. Items are in path order.
func (s *Service) Subtree(ctx context.Context, id uuid.UUID, page int) (SubtreePage, error) {
	if page < 1 {
		page = 1
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return SubtreePage{}, err
	}
	if !set.Enabled {
		return SubtreePage{}, ErrNotFound
	}
	var entityID uuid.UUID
	var path, status string
	var depth int
	err = s.db.Pool().QueryRow(ctx, `SELECT entity_id, path, depth, status FROM hatamoto_comments WHERE id = $1`, id).
		Scan(&entityID, &path, &depth, &status)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !shown(Status(status))) {
		return SubtreePage{}, ErrNotFound
	}
	if err != nil {
		return SubtreePage{}, err
	}
	out := SubtreePage{EntityID: entityID, Page: page, Ancestors: []ThreadComment{}}
	if depth > 1 {
		paths := make([]string, 0, depth-1)
		for d := 1; d < depth; d++ {
			paths = append(paths, AncestorPath(path, d))
		}
		rows, err := s.db.Pool().Query(ctx, `SELECT `+subtreeCols+` FROM hatamoto_comments c
			WHERE c.entity_id = $1 AND c.path = ANY($2::text[]) AND c.status IN ('approved', 'dead') ORDER BY c.path`, entityID, paths)
		if err != nil {
			return SubtreePage{}, err
		}
		if out.Ancestors, err = scanPublic(rows); err != nil {
			return SubtreePage{}, err
		}
		if len(out.Ancestors) != depth-1 {
			return SubtreePage{}, ErrNotFound
		}
	}
	rows, err := s.db.Pool().Query(ctx, subtreeItemsSQL, entityID, path, SubtreePageSize+1, (page-1)*SubtreePageSize)
	if err != nil {
		return SubtreePage{}, err
	}
	cs, err := scanPublic(rows)
	if err != nil {
		return SubtreePage{}, err
	}
	if len(cs) > SubtreePageSize {
		cs, out.HasMore = cs[:SubtreePageSize], true
	}
	out.Comments = cs
	return out, nil
}

// shown reports whether a status is visible in a thread (approved or dead).
func shown(st Status) bool { return st == StatusApproved || st == StatusDead }
