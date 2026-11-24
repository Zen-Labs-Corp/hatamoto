package comment

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/jsonx"
)

// Thread SQL: one statement. threadSelect joins the page of top-level comments
// (approved or dead; the roots CTE of threadRootsSQL) to each root's subtree by
// a path range scan. The root order is picked inside the statement; nothing is
// interpolated.
const threadSelect = `
SELECT c.id, c.parent_id, c.depth, c.author_name, c.author_user_id IS NOT NULL,
       c.body_html, c.created_at, c.edited_at, c.status, c.rank, r.rk, r.roots, (SELECT d FROM dir)
FROM roots r
JOIN hatamoto_comments c
  ON c.entity_id = $1 AND c.status IN ('approved', 'dead')
 AND c.path >= r.path AND c.path < r.path || '/'
ORDER BY r.rk, c.path`

// threadRootsSQL picks the roots in the content type's own order (newest,
// oldest or rank) or, when the type sets none, in the site sort ($4). The order
// is resolved inside the statement, so Thread stays one query; each branch keeps
// its own index-friendly ORDER BY and only one of them is not empty. The
// resolved order comes back on every row so Thread can order the replies.
const threadRootsSQL = `WITH dir AS (
    SELECT COALESCE((SELECT CASE s.settings->'comments'->>'order' WHEN 'newest' THEN 'newest' WHEN 'oldest' THEN 'oldest' WHEN 'rank' THEN 'rank' END
                     FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
                     WHERE e.id = $1), $4::text) AS d
), roots AS (
    (SELECT path, row_number() OVER (ORDER BY created_at DESC, seq DESC) AS rk,
            count(*) OVER () AS roots
     FROM hatamoto_comments
     WHERE entity_id = $1 AND parent_id IS NULL AND status IN ('approved', 'dead') AND (SELECT d FROM dir) = 'newest'
     ORDER BY created_at DESC, seq DESC
     LIMIT $2 OFFSET $3)
    UNION ALL
    (SELECT path, row_number() OVER (ORDER BY created_at ASC, seq ASC) AS rk,
            count(*) OVER () AS roots
     FROM hatamoto_comments
     WHERE entity_id = $1 AND parent_id IS NULL AND status IN ('approved', 'dead') AND (SELECT d FROM dir) = 'oldest'
     ORDER BY created_at ASC, seq ASC
     LIMIT $2 OFFSET $3)
    UNION ALL
    (SELECT path, row_number() OVER (ORDER BY rank DESC, created_at DESC, seq DESC) AS rk,
            count(*) OVER () AS roots
     FROM hatamoto_comments
     WHERE entity_id = $1 AND parent_id IS NULL AND status IN ('approved', 'dead') AND (SELECT d FROM dir) = 'rank'
     ORDER BY rank DESC, created_at DESC, seq DESC
     LIMIT $2 OFFSET $3)
)` + threadSelect

// Thread returns one page of the tree in render order with one query (the
// settings come from the cache): roots by the content type's order (the site
// sort when it sets none), each followed by its subtree in path order, replies
// oldest first (by rank, then newest first, in a rank type). Approved and dead
// comments are returned; a reply whose parent is neither is dropped with its
// subtree. A dead row keeps its BodyHTML and AuthorName: hiding them from
// viewers who may not see them is the caller's job. Only public fields are
// selected.
func (s *Service) Thread(ctx context.Context, entityID uuid.UUID, page int) (ThreadPage, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return ThreadPage{}, err
	}
	if page < 1 {
		page = 1
	}
	site := SortNewest
	if set.Sort == SortOldest {
		site = SortOldest
	}
	offset := (page - 1) * set.PageSize
	rows, err := s.db.Pool().Query(ctx, threadRootsSQL, entityID, set.PageSize+1, offset, string(site))
	if err != nil {
		return ThreadPage{}, err
	}
	defer rows.Close()

	out := ThreadPage{Page: page, Pages: page, Comments: []ThreadComment{}}
	kept := map[uuid.UUID]bool{}
	var order string // the resolved root order, the same on every row
	for rows.Next() {
		var tc ThreadComment
		var status string
		var rk, roots int64
		if err := rows.Scan(&tc.ID, &tc.ParentID, &tc.Depth, &tc.AuthorName, &tc.IsSiteAuthor,
			&tc.BodyHTML, &tc.CreatedAt, &tc.EditedAt, &status, &tc.Rank, &rk, &roots, &order); err != nil {
			return ThreadPage{}, err
		}
		tc.Status = Status(status)
		out.Pages = int((roots + int64(set.PageSize) - 1) / int64(set.PageSize))
		if rk > int64(offset+set.PageSize) { // rk counts from the first root, not from the page
			out.HasMore = true
			continue
		}
		if tc.ParentID != nil && !kept[*tc.ParentID] {
			continue
		}
		kept[tc.ID] = true
		out.Comments = append(out.Comments, tc)
	}
	if err := rows.Err(); err != nil {
		return ThreadPage{}, err
	}
	if Order(order) == OrderRank {
		out.Comments = rankOrder(out.Comments)
	}
	return out, nil
}

// rankOrder re-sorts the replies of a page: roots keep their input order, the
// children of each parent go by rank (highest first), then newest first, then
// id; the result is the depth-first flattening. The input lists every parent
// before its children.
func rankOrder(cs []ThreadComment) []ThreadComment {
	kids := map[uuid.UUID][]ThreadComment{}
	var roots []ThreadComment
	for _, c := range cs {
		if c.ParentID == nil {
			roots = append(roots, c)
			continue
		}
		kids[*c.ParentID] = append(kids[*c.ParentID], c)
	}
	out := make([]ThreadComment, 0, len(cs))
	var walk func(c ThreadComment)
	walk = func(c ThreadComment) {
		out = append(out, c)
		ch := kids[c.ID]
		sort.SliceStable(ch, func(i, j int) bool {
			a, b := ch[i], ch[j]
			if a.Rank != b.Rank {
				return a.Rank > b.Rank
			}
			if !a.CreatedAt.Equal(b.CreatedAt) {
				return a.CreatedAt.After(b.CreatedAt)
			}
			return a.ID.String() < b.ID.String()
		})
		for _, k := range ch {
			walk(k)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	return out
}

// EntityCounts are the approved and pending counts of one entity.
type EntityCounts struct{ Approved, Pending int }

// Counts reads the counts of several entities in one query; a missing id reads as zero.
func (s *Service) Counts(ctx context.Context, entityIDs []uuid.UUID) (map[uuid.UUID]EntityCounts, error) {
	out := make(map[uuid.UUID]EntityCounts, len(entityIDs))
	if len(entityIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Pool().Query(ctx, `SELECT entity_id, comment_count, pending_count
		FROM hatamoto_comment_entities WHERE entity_id = ANY($1)`, entityIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var c EntityCounts
		if err := rows.Scan(&id, &c.Approved, &c.Pending); err != nil {
			return nil, err
		}
		out[id] = c
	}
	for _, id := range entityIDs {
		if _, ok := out[id]; !ok {
			out[id] = EntityCounts{}
		}
	}
	return out, rows.Err()
}

// StatusCounts counts comments per status (the admin badge).
func (s *Service) StatusCounts(ctx context.Context) (map[Status]int, error) {
	rows, err := s.db.Pool().Query(ctx, `SELECT status, count(*)::int FROM hatamoto_comments GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[Status]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[Status(st)] = n
	}
	return out, rows.Err()
}

// QueueFilter narrows the moderation queue; zero values mean "any".
type QueueFilter struct {
	Status      Status
	EntityID    *uuid.UUID
	CommenterID *uuid.UUID
	Trust       TrustLevel
	HasLinks    *bool
	Search      string
	Limit       int
	Offset      int
}

// QueueItem is a comment with its commenter's trust. Comment.SpamReasons says why it was held.
type QueueItem struct {
	Comment       Comment
	Trust         TrustLevel
	ApprovedCount int
}

const (
	defaultQueueLimit = 50
	maxQueueLimit     = 200
)

// qualifiedColumns is commentColumns with a "c." prefix, for joins.
var qualifiedColumns = "c." + strings.ReplaceAll(strings.Join(strings.Fields(commentColumns), " "), ", ", ", c.")

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Queue lists comments newest first for moderation. Search matches body_raw
// case-insensitively with % and _ taken literally.
func (s *Service) Queue(ctx context.Context, f QueueFilter) ([]QueueItem, error) {
	if f.Limit < 1 {
		f.Limit = defaultQueueLimit
	}
	f.Limit = min(f.Limit, maxQueueLimit)
	f.Offset = max(f.Offset, 0)
	search := ""
	if f.Search != "" {
		search = "%" + likeEscaper.Replace(f.Search) + "%"
	}
	rows, err := s.db.Pool().Query(ctx, `SELECT `+qualifiedColumns+`,
			COALESCE(cm.trust_level, 'new'), COALESCE(cm.approved_count, 0)
		FROM hatamoto_comments c
		LEFT JOIN hatamoto_commenters cm ON cm.id = c.commenter_id
		WHERE ($1::text = '' OR c.status = $1)
		  AND ($2::uuid IS NULL OR c.entity_id = $2)
		  AND ($3::uuid IS NULL OR c.commenter_id = $3)
		  AND ($4::text = '' OR cm.trust_level = $4)
		  AND ($5::boolean IS NULL OR c.has_links = $5)
		  AND ($6::text = '' OR c.body_raw ILIKE $6)
		ORDER BY c.created_at DESC, c.seq DESC
		LIMIT $7 OFFSET $8`,
		string(f.Status), f.EntityID, f.CommenterID, string(f.Trust), f.HasLinks, search, f.Limit, f.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QueueItem
	for rows.Next() {
		var c Comment
		var status string
		var reasons []byte
		var trust string
		var deadBy *string
		var item QueueItem
		if err := rows.Scan(&c.ID, &c.EntityID, &c.ParentID, &c.Path, &c.Depth, &status, &c.BodyRaw, &c.BodyHTML,
			&c.HasLinks, &c.LinkDomains, &c.BodyHash, &c.CommenterID, &c.AuthorName, &c.AuthorUserID,
			&c.IPHash, &c.NetHash, &c.UserAgent, &c.SpamScore, &c.SpamVerdict, &reasons, &c.CreatedAt,
			&c.UpdatedAt, &c.ConfirmedAt, &c.ApprovedAt, &c.ApprovedBy, &c.EditedAt, &c.Locale, &c.NotifyReplies,
			&c.Rank, &deadBy, &trust, &item.ApprovedCount); err != nil {
			return nil, err
		}
		c.Status = Status(status)
		if deadBy != nil {
			c.DeadBy = *deadBy
		}
		c.SpamReasons = decodeReasons(reasons)
		item.Comment, item.Trust = c, TrustLevel(trust)
		out = append(out, item)
	}
	return out, rows.Err()
}

// Commenter returns the commenter with this email hash, or ErrNotFound.
func (s *Service) Commenter(ctx context.Context, emailHash string) (Commenter, error) {
	var c Commenter
	var trust string
	err := s.db.Pool().QueryRow(ctx, `SELECT id, email_hash, trust_level, approved_count, first_approved_at,
			last_spam_at, token_version, created_at, last_seen_at
		FROM hatamoto_commenters WHERE email_hash = $1`, emailHash).
		Scan(&c.ID, &c.EmailHash, &trust, &c.ApprovedCount, &c.FirstApprovedAt, &c.LastSpamAt,
			&c.TokenVersion, &c.CreatedAt, &c.LastSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Commenter{}, ErrNotFound
	}
	if err != nil {
		return Commenter{}, err
	}
	c.Trust = TrustLevel(trust)
	return c, nil
}

// EntityConfig is one entity's resolved comment mode plus its content type's
// thread order and community policy.
type EntityConfig struct {
	Mode   Mode
	Order  Order
	Policy Policy
}

// EntityConfig resolves the comment mode, order and policy of one entity in
// one query. With comments off it answers ModeOff (zero Order and Policy)
// without a query. An unknown entity is ErrNotFound. A bad stored order or
// policy reads as unset, never as an error.
func (s *Service) EntityConfig(ctx context.Context, entityID uuid.UUID) (EntityConfig, error) {
	set, err := s.Settings(ctx)
	if err != nil || !set.Enabled {
		return EntityConfig{Mode: ModeOff}, err
	}
	var schemaMode, schemaOrder, entityMode *string
	var policy []byte
	var published *time.Time
	err = s.db.Pool().QueryRow(ctx, `SELECT s.settings->'comments'->>'mode', s.settings->'comments'->>'order',
			s.settings->'comments'->'policy', ce.mode, e.published_at
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		LEFT JOIN hatamoto_comment_entities ce ON ce.entity_id = e.id
		WHERE e.id = $1`, entityID).Scan(&schemaMode, &schemaOrder, &policy, &entityMode, &published)
	if errors.Is(err, pgx.ErrNoRows) {
		return EntityConfig{Mode: ModeOff}, ErrNotFound
	}
	if err != nil {
		return EntityConfig{Mode: ModeOff}, err
	}
	cfg := EntityConfig{
		Mode:   ResolveMode(set, deref(schemaMode), deref(entityMode), published, s.utcNow()),
		Policy: DecodePolicy(policy),
	}
	if ValidOrder(deref(schemaOrder)) {
		cfg.Order = Order(deref(schemaOrder))
	}
	return cfg, nil
}

// EntityMode resolves the comment mode of one entity. With comments off it
// answers ModeOff without a query. An unknown entity is ErrNotFound.
func (s *Service) EntityMode(ctx context.Context, entityID uuid.UUID) (Mode, error) {
	cfg, err := s.EntityConfig(ctx, entityID)
	return cfg.Mode, err
}

// Sort is the root order for a type: newest and oldest override the site sort;
// rank and "" keep it (the rank order is applied by a plugin).
func (o Order) Sort(site Sort) Sort {
	switch o {
	case OrderNewest:
		return SortNewest
	case OrderOldest:
		return SortOldest
	}
	return site
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// SetEntityMode sets the per-entity mode override; "" inherits the schema default.
func (s *Service) SetEntityMode(ctx context.Context, entityID uuid.UUID, m Mode) error {
	var v *string
	switch m {
	case "":
	case ModeOpen, ModeClosed, ModeHidden:
		str := string(m)
		v = &str
	default:
		return &FieldError{Field: "mode", Message: "must be open, closed, hidden or empty"}
	}
	_, err := s.db.Pool().Exec(ctx, `INSERT INTO hatamoto_comment_entities (entity_id, mode, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (entity_id) DO UPDATE SET mode = EXCLUDED.mode, updated_at = EXCLUDED.updated_at`,
		entityID, v, s.utcNow())
	if err != nil {
		return err
	}
	s.obs.each("mode", func(o Observer) { o.OnModeChanged(ctx, entityID) })
	return nil
}

// TypeModeChanged tells the observers that a type's default comment mode
// changed. The default lives in the schema settings, written by the caller.
func (s *Service) TypeModeChanged(ctx context.Context) {
	s.obs.each("mode", func(o Observer) { o.OnModeChanged(ctx, uuid.Nil) })
}

func decodeReasons(raw []byte) []string {
	var out []string
	if len(raw) == 0 || jsonx.Unmarshal(raw, &out, jsonx.StoredLoose) != nil {
		return nil
	}
	return out
}
