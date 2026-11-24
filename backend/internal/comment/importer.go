package comment

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/database"
)

// ImportedComment is one comment from another system, already formatted by the
// caller with commentfmt.Render (this package does not import commentfmt).
// There is no author URL field on purpose.
type ImportedComment struct {
	Key               string     // caller key, unique in the batch (for example the source comment id)
	ParentKey         string     // parent in this batch; "" = none
	ParentID          *uuid.UUID // parent imported earlier (re-run); used when ParentKey is "" or unknown
	EntityID          uuid.UUID
	AuthorName        string
	Email             string // raw: normalised, hashed and sealed here, never stored plain
	IP                string // raw: only the Keys.HashIP output is stored
	BodyRaw, BodyHTML string
	HasLinks          bool
	LinkDomains       []string
	BodyHash          string
	Status            Status    // pending, approved, spam or trash; draft is refused
	CreatedAt         time.Time // UTC
}

// ImportStats reports what one Import call did.
type ImportStats struct {
	IDs               map[string]uuid.UUID // Key -> new comment id
	CommentersCreated int
	CommentersUpdated int
	Reparented        int // parent not found on this entity: became top level
	Clamped           int // attached higher than the real parent (max depth)
}

// Importer writes historical comments silently: it keeps the original
// timestamps, fires no observer and never goes through the Service write
// methods, so an import sends no notification and runs no spam check.
type Importer struct {
	db       *database.DB
	keys     Keys
	settings *SettingsStore
	now      func() time.Time
}

// NewImporter returns an Importer; serverSecret must be the Service's secret so
// the hashes match the live ones.
func NewImporter(db *database.DB, serverSecret string) *Importer {
	return &Importer{db: db, keys: NewKeys(serverSecret), settings: NewSettingsStore(db.Pool()), now: time.Now}
}

type importedParent struct {
	id   uuid.UUID
	path string
}

type importedCommenter struct {
	hash    string
	sealed  []byte
	created time.Time
	id      uuid.UUID
}

// Import writes one entity's comments (parents before children) in ONE
// transaction. Commenter trust is derived from the imported history and capped
// at known; a blocked or vouched commenter keeps their level.
func (im *Importer) Import(ctx context.Context, batch []ImportedComment) (ImportStats, error) {
	stats := ImportStats{IDs: map[string]uuid.UUID{}}
	if len(batch) == 0 {
		return stats, nil
	}
	entity := batch[0].EntityID
	seen := map[string]bool{}
	hashes := make([]string, len(batch))
	commenters := map[string]*importedCommenter{}
	for i, b := range batch {
		if b.EntityID != entity {
			return stats, fmt.Errorf("comment import: %q is for another entity", b.Key)
		}
		if b.Key == "" || seen[b.Key] {
			return stats, fmt.Errorf("comment import: empty or duplicate key %q", b.Key)
		}
		seen[b.Key] = true
		switch b.Status {
		case StatusPending, StatusApproved, StatusSpam, StatusTrash, StatusDead:
		default:
			return stats, fmt.Errorf("comment import: %q has status %q", b.Key, b.Status)
		}
		if e := NormalizeEmail(b.Email); e == "" || !strings.Contains(e, "@") {
			return stats, fmt.Errorf("comment import: %q has no valid email", b.Key)
		}
		h := im.keys.HashEmail(b.Email)
		hashes[i] = h
		created := b.CreatedAt.UTC()
		if c, ok := commenters[h]; ok {
			if created.Before(c.created) {
				c.created = created
			}
			continue
		}
		sealed, err := im.keys.SealEmail(b.Email, h)
		if err != nil {
			return stats, fmt.Errorf("comment import: %q: seal email: %w", b.Key, err)
		}
		commenters[h] = &importedCommenter{hash: h, sealed: sealed, created: created}
	}
	set, err := im.settings.Get(ctx)
	if err != nil {
		return stats, err
	}
	// The import is scoped to one entity: resolve its type's reply depth once.
	// An unknown entity keeps the site depth; the insert then fails on its own.
	_, pol, err := schemaComments(ctx, im.db.Pool(), entity)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return stats, err
	}
	maxDepth := pol.Depth(set.MaxDepth)
	now := im.now().UTC()
	order := make([]*importedCommenter, 0, len(commenters))
	for _, c := range commenters {
		order = append(order, c)
	}
	sort.Slice(order, func(i, j int) bool { return order[i].hash < order[j].hash })

	err = im.db.WithTx(ctx, func(tx pgx.Tx) error {
		// Same lock order as Service.Store: comment rows, counts row, commenter rows.
		// Every existing row a reply can attach to is locked here: the parent and
		// its ancestors, since max depth clamps a reply to an ancestor.
		existing := map[uuid.UUID]importedParent{}
		for _, b := range batch {
			if b.ParentID == nil {
				continue
			}
			if _, ok := existing[*b.ParentID]; ok {
				continue
			}
			var pe uuid.UUID
			var path string
			err := tx.QueryRow(ctx, `SELECT entity_id, path FROM hatamoto_comments WHERE id = $1 FOR KEY SHARE`, *b.ParentID).Scan(&pe, &path)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && pe != entity) {
				continue
			}
			if err != nil {
				return err
			}
			if d := Depth(path); d > 1 {
				anc := make([]string, 0, d-1)
				for i := 1; i < d; i++ {
					anc = append(anc, AncestorPath(path, i))
				}
				if _, err := tx.Exec(ctx, `SELECT id FROM hatamoto_comments WHERE path = ANY($1) ORDER BY path FOR KEY SHARE`, anc); err != nil {
					return err
				}
			}
			existing[*b.ParentID] = importedParent{id: *b.ParentID, path: path}
		}
		if err := lockCounts(ctx, tx, entity); err != nil {
			return err
		}
		for _, c := range order {
			var id uuid.UUID
			err := tx.QueryRow(ctx, `INSERT INTO hatamoto_commenters (email_hash, email_sealed, created_at, last_seen_at)
				VALUES ($1, $2, $3, $3) ON CONFLICT (email_hash) DO NOTHING RETURNING id`, c.hash, c.sealed, c.created).Scan(&id)
			switch {
			case err == nil:
				stats.CommentersCreated++
			case errors.Is(err, pgx.ErrNoRows):
				if err := tx.QueryRow(ctx, `SELECT id FROM hatamoto_commenters WHERE email_hash = $1`, c.hash).Scan(&id); err != nil {
					return err
				}
				stats.CommentersUpdated++
			default:
				return err
			}
			c.id = id
		}

		inBatch := map[string]importedParent{}
		for i, b := range batch {
			var parent *importedParent
			if p, ok := inBatch[b.ParentKey]; ok && b.ParentKey != "" {
				parent = &p
			} else if b.ParentID != nil {
				if p, ok := existing[*b.ParentID]; ok {
					parent = &p
				}
			}
			var parentID *uuid.UUID
			parentPath, depth := "", 1
			if parent != nil {
				attach := ReplyParentPath(parent.path, maxDepth)
				if attach != parent.path {
					stats.Clamped++
				}
				if attach != "" {
					var id uuid.UUID
					if err := tx.QueryRow(ctx, `SELECT id FROM hatamoto_comments WHERE path = $1 FOR KEY SHARE`, attach).Scan(&id); err != nil {
						return err
					}
					parentID, parentPath, depth = &id, attach, Depth(attach)+1
				}
			} else if b.ParentKey != "" || b.ParentID != nil {
				stats.Reparented++
			}

			ipHash, netHash := im.keys.HashIP(b.IP)
			created := b.CreatedAt.UTC()
			var approvedAt *time.Time
			if b.Status == StatusApproved {
				approvedAt = &created
			}
			verdict := "allow"
			if b.Status == StatusSpam {
				verdict = "spam"
			}
			var id uuid.UUID
			var path string
			err := tx.QueryRow(ctx, `
				WITH s AS (SELECT nextval('hatamoto_comments_seq') AS v)
				INSERT INTO hatamoto_comments (seq, entity_id, parent_id, path, depth, status, body_raw, body_html,
					has_links, link_domains, body_hash, commenter_id, author_name, ip_hash, net_hash,
					user_agent, spam_verdict, spam_reasons, created_at, updated_at, confirmed_at, approved_at)
				SELECT s.v, $1, $2,
					CASE WHEN $3::text = '' THEN '' ELSE $3::text || '.' END || lpad(to_hex(s.v), 12, '0'),
					$4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, '', $15, '[]'::jsonb, $16, $16, $16, $17
				FROM s
				RETURNING id, path`,
				entity, parentID, parentPath, depth, string(b.Status), b.BodyRaw, b.BodyHTML,
				b.HasLinks, nonNil(b.LinkDomains), b.BodyHash, commenters[hashes[i]].id,
				truncRunes(b.AuthorName, maxAuthorName), ipHash, netHash, verdict, created, approvedAt).Scan(&id, &path)
			if err != nil {
				return err
			}
			inBatch[b.Key] = importedParent{id: id, path: path}
			stats.IDs[b.Key] = id
		}
		if err := recount(ctx, tx, entity, now); err != nil {
			return err
		}
		for _, c := range order {
			if err := im.refreshImported(ctx, tx, c.id, set.Thresholds, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ImportStats{IDs: map[string]uuid.UUID{}}, err
	}
	return stats, nil
}

// refreshImported folds a commenter's imported history into their row. Unlike
// refreshCommenter it uses the real approval dates, and it never lifts trust
// above known: a historical import cannot make someone trusted.
func (im *Importer) refreshImported(ctx context.Context, tx pgx.Tx, id uuid.UUID, th Thresholds, now time.Time) error {
	var one int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM hatamoto_commenters WHERE id = $1 FOR NO KEY UPDATE`, id).Scan(&one); err != nil {
		return err
	}
	var level string
	st := TrustStats{}
	err := tx.QueryRow(ctx, `UPDATE hatamoto_commenters c SET
			approved_count = x.n,
			first_approved_at = LEAST(c.first_approved_at, x.fa),
			last_spam_at = GREATEST(c.last_spam_at, x.ls),
			last_seen_at = GREATEST(c.last_seen_at, x.last)
		FROM (SELECT count(*) FILTER (WHERE status = 'approved')::int AS n,
				min(approved_at) FILTER (WHERE status = 'approved') AS fa,
				max(created_at) FILTER (WHERE status = 'spam') AS ls,
				max(created_at) AS last
			FROM hatamoto_comments WHERE commenter_id = $1) x
		WHERE c.id = $1
		RETURNING c.trust_level, c.approved_count, c.first_approved_at, c.last_spam_at`, id).
		Scan(&level, &st.ApprovedCount, &st.FirstApprovedAt, &st.LastSpamAt)
	if err != nil {
		return err
	}
	st.Current = TrustLevel(level)
	next := EvaluateTrust(TrustStats{Current: TrustNew, ApprovedCount: st.ApprovedCount,
		FirstApprovedAt: st.FirstApprovedAt, LastSpamAt: st.LastSpamAt}, th, now)
	if next.rank() > TrustKnown.rank() {
		next = TrustKnown
	}
	if st.Current == TrustBlocked || next.rank() <= st.Current.rank() {
		return nil
	}
	_, err = tx.Exec(ctx, `UPDATE hatamoto_commenters SET trust_level = $2 WHERE id = $1`, id, string(next))
	return err
}
