package comment

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	maxAuthorName = 100
	maxUserAgent  = 256
)

func truncRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// lockAndRecount recomputes the approved and pending counts of one entity. It
// locks the entity's counts row first, so concurrent moderation cannot write
// a stale count. It never touches hatamoto_entities.
func lockAndRecount(ctx context.Context, tx pgx.Tx, entityID uuid.UUID, now time.Time) error {
	if err := lockCounts(ctx, tx, entityID); err != nil {
		return err
	}
	return recount(ctx, tx, entityID, now)
}

// lockCounts takes the entity's counts row lock, creating the row if needed.
func lockCounts(ctx context.Context, tx pgx.Tx, entityID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `INSERT INTO hatamoto_comment_entities (entity_id) VALUES ($1) ON CONFLICT DO NOTHING`, entityID); err != nil {
		return err
	}
	var one int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM hatamoto_comment_entities WHERE entity_id = $1 FOR UPDATE`, entityID).Scan(&one); err != nil {
		return err
	}
	return nil
}

func recount(ctx context.Context, tx pgx.Tx, entityID uuid.UUID, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE hatamoto_comment_entities SET
			comment_count = (SELECT count(*) FROM hatamoto_comments WHERE entity_id = $1 AND status = 'approved'),
			pending_count = (SELECT count(*) FROM hatamoto_comments WHERE entity_id = $1 AND status = 'pending'),
			updated_at = $2
		WHERE entity_id = $1`, entityID, now)
	return err
}

// refreshCommenter recomputes a commenter's approved count, first approval and
// trust level from their comments. It locks the commenter row first and counts
// in a later statement, so a concurrent approval on another entity is seen.
func refreshCommenter(ctx context.Context, tx pgx.Tx, id uuid.UUID, th Thresholds, now time.Time) error {
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM hatamoto_commenters WHERE id = $1 FOR NO KEY UPDATE`, id).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	st := TrustStats{}
	var level string
	err = tx.QueryRow(ctx, `UPDATE hatamoto_commenters c SET
			approved_count = x.n,
			first_approved_at = COALESCE(c.first_approved_at, CASE WHEN x.n > 0 THEN $2::timestamptz END)
		FROM (SELECT count(*)::int AS n FROM hatamoto_comments WHERE commenter_id = $1 AND status = 'approved') x
		WHERE c.id = $1
		RETURNING c.trust_level, c.approved_count, c.first_approved_at, c.last_spam_at`, id, now).
		Scan(&level, &st.ApprovedCount, &st.FirstApprovedAt, &st.LastSpamAt)
	if err != nil {
		return err
	}
	st.Current = TrustLevel(level)
	if next := EvaluateTrust(st, th, now); next != st.Current {
		_, err = tx.Exec(ctx, `UPDATE hatamoto_commenters SET trust_level = $2 WHERE id = $1`, id, string(next))
	}
	return err
}

func selectForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Comment, error) {
	c, err := scanComment(tx.QueryRow(ctx, `SELECT `+commentColumns+` FROM hatamoto_comments WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Comment{}, ErrNotFound
	}
	return c, err
}

func countsStatus(s Status) bool { return s == StatusApproved || s == StatusPending }

// Store saves an already formatted and scored draft. The email is hashed and
// sealed, the IP is reduced to two pseudonymous hashes; neither is stored plain.
// A reply deeper than the max depth attaches to its ancestor at max depth - 1.
func (s *Service) Store(ctx context.Context, d Draft) (Comment, error) {
	switch d.Status {
	case StatusDraft, StatusPending, StatusApproved, StatusSpam:
	default:
		return Comment{}, fmt.Errorf("comment: cannot store a comment as %q", d.Status)
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return Comment{}, err
	}
	now := s.utcNow()
	ipHash, netHash := s.keys.HashIP(d.IP)
	reasons, err := reasonsJSON(d.SpamReasons)
	if err != nil {
		return Comment{}, err
	}
	verdict := d.SpamVerdict
	if verdict == "" {
		verdict = "allow"
	}

	var out Comment
	err = s.db.WithTx(ctx, func(tx pgx.Tx) error {
		// Lock order everywhere: comment rows, entity counts row, commenter row.
		// The INSERT takes KEY SHARE locks on the parent comment and the
		// commenter row (foreign keys); transition, Delete, Edit and Erase lock
		// the comment before the counts row, so the parent is locked first here
		// and the counts row has to be locked before the commenter.
		var parentID *uuid.UUID
		parentPath, depth := "", 1
		if d.ParentID != nil {
			var entity uuid.UUID
			var path string
			err := tx.QueryRow(ctx, `SELECT entity_id, path FROM hatamoto_comments WHERE id = $1 FOR KEY SHARE`, *d.ParentID).Scan(&entity, &path)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && entity != d.EntityID) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			_, pol, err := schemaComments(ctx, tx, d.EntityID)
			if err != nil {
				return err
			}
			if attach := ReplyParentPath(path, pol.Depth(set.MaxDepth)); attach != "" {
				var id uuid.UUID
				if err := tx.QueryRow(ctx, `SELECT id FROM hatamoto_comments WHERE path = $1 FOR KEY SHARE`, attach).Scan(&id); err != nil {
					return err
				}
				parentID, parentPath, depth = &id, attach, Depth(attach)+1
			}
		}

		if countsStatus(d.Status) {
			if err := lockCounts(ctx, tx, d.EntityID); err != nil {
				return err
			}
		}
		var commenterID *uuid.UUID
		if d.Email != "" {
			h := s.keys.HashEmail(d.Email)
			sealed, err := s.keys.SealEmail(d.Email, h)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO hatamoto_commenters (email_hash, email_sealed, created_at, last_seen_at)
				VALUES ($1, $2, $3, $3) ON CONFLICT (email_hash) DO NOTHING`, h, sealed, now); err != nil {
				return err
			}
			var id uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM hatamoto_commenters WHERE email_hash = $1`, h).Scan(&id); err != nil {
				return err
			}
			commenterID = &id
		} else if d.CommenterID != nil {
			commenterID = d.CommenterID
		}

		var confirmedAt, approvedAt *time.Time
		var approvedBy *uuid.UUID
		if d.Status != StatusDraft {
			confirmedAt = &now
		}
		if d.Status == StatusApproved {
			approvedAt, approvedBy = &now, d.AuthorUserID
		}

		c, err := scanComment(tx.QueryRow(ctx, `
			WITH s AS (SELECT nextval('hatamoto_comments_seq') AS v)
			INSERT INTO hatamoto_comments (seq, entity_id, parent_id, path, depth, status, body_raw, body_html,
				has_links, link_domains, body_hash, commenter_id, author_name, author_user_id, ip_hash, net_hash,
				user_agent, spam_score, spam_verdict, spam_reasons, created_at, updated_at, confirmed_at,
				approved_at, approved_by, locale, notify_replies)
			SELECT s.v, $1, $2,
				CASE WHEN $3::text = '' THEN '' ELSE $3::text || '.' END || lpad(to_hex(s.v), 12, '0'),
				$4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19::jsonb, $20, $20, $21, $22, $23, $24, $25
			FROM s
			RETURNING `+commentColumns,
			d.EntityID, parentID, parentPath, depth, string(d.Status), d.BodyRaw, d.BodyHTML,
			d.HasLinks, nonNil(d.LinkDomains), d.BodyHash, commenterID,
			truncRunes(d.AuthorName, maxAuthorName), d.AuthorUserID, ipHash, netHash,
			truncRunes(d.UserAgent, maxUserAgent), d.SpamScore, verdict, reasons,
			now, confirmedAt, approvedAt, approvedBy, d.Locale, d.NotifyReplies && d.Email != ""))
		if err != nil {
			return err
		}
		if countsStatus(c.Status) {
			if err := recount(ctx, tx, c.EntityID, now); err != nil {
				return err
			}
		}
		if c.CommenterID != nil {
			if _, err := tx.Exec(ctx, `UPDATE hatamoto_commenters SET last_seen_at = $2 WHERE id = $1`, *c.CommenterID, now); err != nil {
				return err
			}
			if c.Status == StatusApproved {
				if err := refreshCommenter(ctx, tx, *c.CommenterID, set.Thresholds, now); err != nil {
					return err
				}
			}
		}
		out = c
		return nil
	})
	if err != nil {
		return Comment{}, err
	}
	s.obs.each("created", func(o Observer) { o.OnCreated(ctx, out) })
	s.fireStatus(ctx, out)
	return out, nil
}

// fireStatus tells the observers about a comment that now has its final status.
func (s *Service) fireStatus(ctx context.Context, c Comment) {
	switch c.Status {
	case StatusApproved:
		s.obs.each("approved", func(o Observer) { o.OnApproved(ctx, c) })
	case StatusSpam:
		s.obs.each("spam", func(o Observer) { o.OnSpam(ctx, c) })
	}
}

// Confirm moves a draft to the status Decide picks (pending, approved or spam).
// A draft is the only status it accepts; others return ErrBadTransition.
// Trust is evaluated at the moment of confirmation.
func (s *Service) Confirm(ctx context.Context, id uuid.UUID) (Comment, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return Comment{}, err
	}
	now := s.utcNow()
	var out Comment
	err = s.db.WithTx(ctx, func(tx pgx.Tx) error {
		c, err := selectForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if c.Status != StatusDraft {
			return ErrBadTransition
		}
		trust := TrustNew
		if c.CommenterID != nil {
			var level string
			st := TrustStats{}
			if err := tx.QueryRow(ctx, `SELECT trust_level, approved_count, first_approved_at, last_spam_at
				FROM hatamoto_commenters WHERE id = $1`, *c.CommenterID).
				Scan(&level, &st.ApprovedCount, &st.FirstApprovedAt, &st.LastSpamAt); err != nil {
				return err
			}
			st.Current = TrustLevel(level)
			trust = EvaluateTrust(st, set.Thresholds, now)
		}
		_, pol, err := schemaComments(ctx, tx, c.EntityID)
		if err != nil {
			return err
		}
		status, decided := Decide(DecideInput{
			Verdict: c.SpamVerdict, HasLinks: c.HasLinks, Trust: trust, AdminAuthor: c.AuthorUserID != nil,
			Community: pol.Enabled,
		})
		reasons := mergeReasons(c.SpamReasons, decided)
		reasonsText, err := reasonsJSON(reasons)
		if err != nil {
			return err
		}
		var approvedAt *time.Time
		var approvedBy *uuid.UUID
		if status == StatusApproved {
			approvedAt, approvedBy = &now, c.AuthorUserID
		}
		out, err = scanComment(tx.QueryRow(ctx, `UPDATE hatamoto_comments SET status = $2, confirmed_at = $3,
				updated_at = $3, approved_at = $4, approved_by = $5, spam_reasons = $6::jsonb
			WHERE id = $1 RETURNING `+commentColumns, id, string(status), now, approvedAt, approvedBy, reasonsText))
		if err != nil {
			return err
		}
		if countsStatus(status) {
			if err := lockAndRecount(ctx, tx, out.EntityID, now); err != nil {
				return err
			}
		}
		if status == StatusApproved && out.CommenterID != nil {
			return refreshCommenter(ctx, tx, *out.CommenterID, set.Thresholds, now)
		}
		return nil
	})
	if err != nil {
		return Comment{}, err
	}
	s.obs.each("confirmed", func(o Observer) { o.OnConfirmed(ctx, out) })
	s.fireStatus(ctx, out)
	return out, nil
}

func mergeReasons(have, add []string) []string {
	out := append([]string(nil), have...)
	for _, r := range add {
		dup := false
		for _, h := range out {
			if h == r {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, r)
		}
	}
	return out
}

// afterFunc runs inside a transition's transaction, after the status update.
// before is the comment as it was, c as it is now.
type afterFunc func(ctx context.Context, tx pgx.Tx, before, c Comment, now time.Time) error

// transition moves a comment to another status through CanTransition, keeps the
// entity counts and the commenter's trust right in the same transaction and
// fires the observers after commit. It never sets confirmed_at: only Confirm and
// Store with a non-draft status do, so it keeps meaning "the email is proven".
// after may be nil.
func (s *Service) transition(ctx context.Context, id uuid.UUID, to Status, by *uuid.UUID, after afterFunc) (Comment, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return Comment{}, err
	}
	now := s.utcNow()
	var out, before Comment
	err = s.db.WithTx(ctx, func(tx pgx.Tx) error {
		c, err := selectForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		before = c
		if !CanTransition(c.Status, to) {
			return ErrBadTransition
		}
		var approvedAt *time.Time
		var approvedBy *uuid.UUID
		if to == StatusApproved {
			approvedAt, approvedBy = &now, by
		}
		out, err = scanComment(tx.QueryRow(ctx, `UPDATE hatamoto_comments SET status = $2, updated_at = $3,
				approved_at = CASE WHEN $2::text = 'approved' THEN $4 ELSE approved_at END,
				approved_by = CASE WHEN $2::text = 'approved' THEN $5 ELSE approved_by END
			WHERE id = $1 RETURNING `+commentColumns, id, string(to), now, approvedAt, approvedBy))
		if err != nil {
			return err
		}
		if countsStatus(c.Status) || countsStatus(to) {
			if err := lockAndRecount(ctx, tx, out.EntityID, now); err != nil {
				return err
			}
		}
		if after != nil {
			if err := after(ctx, tx, before, out, now); err != nil {
				return err
			}
		}
		if (c.Status == StatusApproved || to == StatusApproved) && out.CommenterID != nil {
			return refreshCommenter(ctx, tx, *out.CommenterID, set.Thresholds, now)
		}
		return nil
	})
	if err != nil {
		return Comment{}, err
	}
	switch {
	case before.Status == StatusDead && to == StatusApproved, to == StatusDead, to == StatusPending:
		// a restore is an update, not an approval: no comment.approved, no reply mail
		s.obs.each("updated", func(o Observer) { o.OnUpdated(ctx, before, out) })
	case to == StatusApproved:
		s.obs.each("approved", func(o Observer) { o.OnApproved(ctx, out) })
	case to == StatusSpam:
		s.obs.each("spam", func(o Observer) { o.OnSpam(ctx, out) })
	case to == StatusTrash:
		s.obs.each("deleted", func(o Observer) { o.OnDeleted(ctx, out) })
	}
	return out, nil
}

// Approve publishes a comment; by is the moderator, nil for the system.
func (s *Service) Approve(ctx context.Context, id uuid.UUID, by *uuid.UUID) (Comment, error) {
	return s.transition(ctx, id, StatusApproved, by, nil)
}

// Trash moves a comment to the trash (kept until the retention job purges it).
func (s *Service) Trash(ctx context.Context, id uuid.UUID) (Comment, error) {
	return s.transition(ctx, id, StatusTrash, nil, nil)
}

// Delete removes a comment for good; its replies go with it (foreign key cascade).
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	set, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	now := s.utcNow()
	var gone Comment
	var replies []Comment
	err = s.db.WithTx(ctx, func(tx pgx.Tx) error {
		c, err := selectForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		gone = c
		subtree, err := tx.Query(ctx, `SELECT `+commentColumns+` FROM hatamoto_comments
			WHERE path > $1 AND path < $1 || '/' AND status <> 'draft' ORDER BY path`, c.Path)
		if err != nil {
			return err
		}
		if replies, err = collectComments(subtree); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT DISTINCT commenter_id FROM hatamoto_comments
			WHERE commenter_id IS NOT NULL AND path >= $1 AND path < $1 || '/'
			ORDER BY commenter_id`, c.Path)
		if err != nil {
			return err
		}
		var commenters []uuid.UUID
		for rows.Next() {
			var cid uuid.UUID
			if err := rows.Scan(&cid); err != nil {
				rows.Close()
				return err
			}
			commenters = append(commenters, cid)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_comments WHERE id = $1`, id); err != nil {
			return err
		}
		if err := lockAndRecount(ctx, tx, c.EntityID, now); err != nil {
			return err
		}
		for _, cid := range commenters {
			if err := refreshCommenter(ctx, tx, cid, set.Thresholds, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.obs.each("deleted", func(o Observer) { o.OnDeleted(ctx, gone) })
	s.announceDeleted(ctx, replies)
	return nil
}

// Edit stores a re-rendered body and sets edited_at. An edit by the commenter
// that leaves links in the body, or that the content checks held, moves an
// approved comment back to pending. On a content type with the community
// policy a link does not re-hold; a content-check hold still does.
func (s *Service) Edit(ctx context.Context, id uuid.UUID, e Edited) (Comment, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return Comment{}, err
	}
	now := s.utcNow()
	var out, before Comment
	err = s.db.WithTx(ctx, func(tx pgx.Tx) error {
		c, err := selectForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		before = c
		_, pol, err := schemaComments(ctx, tx, c.EntityID)
		if err != nil {
			return err
		}
		status := c.Status
		linkHold := e.HasLinks && !pol.Enabled
		if !e.ByAdmin && (linkHold || e.Hold) && c.Status == StatusApproved {
			status = StatusPending
		}
		out, err = scanComment(tx.QueryRow(ctx, `UPDATE hatamoto_comments SET body_raw = $2, body_html = $3,
				has_links = $4, link_domains = $5, body_hash = $6, status = $7, edited_at = $8, updated_at = $8,
				approved_at = CASE WHEN $7 = 'approved' THEN approved_at END
			WHERE id = $1 RETURNING `+commentColumns,
			id, e.BodyRaw, e.BodyHTML, e.HasLinks, nonNil(e.LinkDomains), e.BodyHash, string(status), now))
		if err != nil {
			return err
		}
		if status == c.Status {
			return nil
		}
		if err := lockAndRecount(ctx, tx, out.EntityID, now); err != nil {
			return err
		}
		if out.CommenterID != nil {
			return refreshCommenter(ctx, tx, *out.CommenterID, set.Thresholds, now)
		}
		return nil
	})
	if err != nil {
		return Comment{}, err
	}
	s.obs.each("updated", func(o Observer) { o.OnUpdated(ctx, before, out) })
	return out, nil
}
