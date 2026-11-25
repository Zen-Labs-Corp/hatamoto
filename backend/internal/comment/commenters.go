package comment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Vouch raises a commenter to known or trusted; it also unblocks. The level is
// a floor: later evaluations never take it back down. Other levels are an error.
func (s *Service) Vouch(ctx context.Context, commenterID uuid.UUID, level TrustLevel) error {
	if level != TrustKnown && level != TrustTrusted {
		return &FieldError{Field: "level", Message: "must be known or trusted"}
	}
	tag, err := s.db.Pool().Exec(ctx, `UPDATE hatamoto_commenters SET trust_level = $2 WHERE id = $1`, commenterID, string(level))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ForgetPlugin detaches every commenter plugin vouches for: the origin is
// cleared, so a later package with the same name cannot reach the accounts,
// and token_version is bumped, so the sessions it vouched for stop working. An
// email-less commenter keeps its 'plugin:<uuid>' key (never an address hash, so
// no typed email reaches it) and, with no origin, falls under the idle purge.
func ForgetPlugin(ctx context.Context, db interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, plugin string) error {
	_, err := db.Exec(ctx, `UPDATE hatamoto_commenters SET origin_plugin = NULL, origin_account = NULL,
		token_version = token_version + 1 WHERE origin_plugin = $1`, plugin)
	return err
}

// Block sets a commenter to blocked and revokes every device cookie.
func (s *Service) Block(ctx context.Context, commenterID uuid.UUID) error {
	tag, err := s.db.Pool().Exec(ctx, `UPDATE hatamoto_commenters SET trust_level = 'blocked',
		token_version = token_version + 1 WHERE id = $1`, commenterID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeDevices bumps token_version, which invalidates every device cookie of
// the commenter, and returns the new value.
func (s *Service) RevokeDevices(ctx context.Context, commenterID uuid.UUID) (int, error) {
	var v int
	err := s.db.Pool().QueryRow(ctx, `UPDATE hatamoto_commenters SET token_version = token_version + 1
		WHERE id = $1 RETURNING token_version`, commenterID).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return v, err
}

// Learn says what MarkSpam adds to the blocklist (rows with source "spam").
// EmailDomain blocks every future commenter of that domain: use it for
// throwaway domains, not for a big mail provider.
type Learn struct {
	EmailDomain bool
	LinkDomains bool
	Hashes      bool // body_hash, ip_hash and net_hash
}

// MarkSpam moves a comment to spam, blocks its commenter (last_spam_at set, device
// cookies revoked; only when the comment was confirmed, a draft's email is unproven) and, as asked, learns from it. Fires OnSpam.
func (s *Service) MarkSpam(ctx context.Context, id uuid.UUID, by *uuid.UUID, learn Learn) (Comment, error) {
	return s.transition(ctx, id, StatusSpam, by, func(ctx context.Context, tx pgx.Tx, before, c Comment, now time.Time) error {
		var values []Block
		add := func(kind BlockKind, v string) {
			if v != "" {
				values = append(values, Block{Kind: kind, Value: v})
			}
		}
		// Only a confirmed address is proven to belong to the commenter; a draft
		// may carry anyone's email, so it must not block or domain-block it.
		if c.CommenterID != nil && before.ConfirmedAt != nil {
			var hash string
			var sealed []byte
			if err := tx.QueryRow(ctx, `UPDATE hatamoto_commenters SET trust_level = 'blocked', last_spam_at = $2,
					token_version = token_version + 1
				WHERE id = $1 RETURNING email_hash, email_sealed`, *c.CommenterID, now).Scan(&hash, &sealed); err != nil {
				return err
			}
			if learn.EmailDomain {
				if email, ok := s.keys.OpenEmail(sealed, hash); ok {
					add(BlockEmailDomain, EmailDomain(email))
				}
			}
		}
		if learn.LinkDomains {
			for _, d := range c.LinkDomains {
				add(BlockLinkDomain, normalizeDomain(d))
			}
		}
		if learn.Hashes {
			add(BlockBodyHash, c.BodyHash)
			add(BlockIPHash, c.IPHash)
			add(BlockNetHash, c.NetHash)
		}
		for _, b := range values {
			if _, err := tx.Exec(ctx, `INSERT INTO hatamoto_comment_blocks (kind, value, source, note, created_at, created_by)
				VALUES ($1, $2, 'spam', '', $3, $4) ON CONFLICT (kind, value) DO NOTHING`,
				string(b.Kind), b.Value, now, by); err != nil {
				return err
			}
		}
		return nil
	})
}

// Export is everything stored about one email address.
type Export struct {
	Commenter Commenter
	Email     string // empty when the sealed address cannot be opened (the server secret changed)
	Comments  []Comment
}

// commenterRowBy reads one commenter by a column. where is "email_hash" or
// "id": a constant of this file, never user input.
func (s *Service) commenterRowBy(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, where string, arg any) (Commenter, []byte, error) {
	var c Commenter
	var trust string
	var sealed []byte
	err := q.QueryRow(ctx, `SELECT id, email_hash, email_sealed, trust_level, approved_count, first_approved_at,
			last_spam_at, token_version, created_at, last_seen_at, coalesce(origin_plugin, '')
		FROM hatamoto_commenters WHERE `+where+` = $1`, arg).
		Scan(&c.ID, &c.EmailHash, &sealed, &trust, &c.ApprovedCount, &c.FirstApprovedAt, &c.LastSpamAt,
			&c.TokenVersion, &c.CreatedAt, &c.LastSeenAt, &c.OriginPlugin)
	if errors.Is(err, pgx.ErrNoRows) {
		return Commenter{}, nil, ErrNotFound
	}
	c.Trust = TrustLevel(trust)
	return c, sealed, err
}

// ExportByEmail returns the commenter record and every comment of one address
// (a data-subject access request). Unknown address: ErrNotFound.
func (s *Service) ExportByEmail(ctx context.Context, email string) (Export, error) {
	c, _, err := s.commenterRowBy(ctx, s.db.Pool(), "email_hash", s.keys.HashEmail(email))
	if err != nil {
		return Export{}, err
	}
	return s.ExportByID(ctx, c.ID)
}

// ExportByID is ExportByEmail for a commenter known by id, including one a
// plugin vouches for (Email is then empty). Unknown id: ErrNotFound.
func (s *Service) ExportByID(ctx context.Context, id uuid.UUID) (Export, error) {
	c, sealed, err := s.commenterRowBy(ctx, s.db.Pool(), "id", id)
	if err != nil {
		return Export{}, err
	}
	out := Export{Commenter: c}
	out.Email, _ = s.keys.OpenEmail(sealed, c.EmailHash)
	rows, err := s.db.Pool().Query(ctx, `SELECT `+commentColumns+` FROM hatamoto_comments
		WHERE commenter_id = $1 ORDER BY created_at, seq`, c.ID)
	if err != nil {
		return Export{}, err
	}
	defer rows.Close()
	for rows.Next() {
		cm, err := scanComment(rows)
		if err != nil {
			return Export{}, err
		}
		out.Comments = append(out.Comments, cm)
	}
	return out, rows.Err()
}

// EraseResult counts what EraseByEmail did.
type EraseResult struct{ Deleted, Anonymised int }

// deletedBody replaces the body of an anonymised comment that still has replies.
const deletedBody = "[deleted]"

// EraseByEmail erases one address: comments nobody replied to are deleted, those
// with replies are anonymised (no name, body replaced, hashes and agent cleared)
// so the thread keeps its shape, and the commenter row is deleted. Entity counts
// are recomputed, OnDeleted fires for each deleted comment that was ever announced (pending included) and OnUpdated for each
// anonymised one (same status, new content). Unknown: ErrNotFound.
func (s *Service) EraseByEmail(ctx context.Context, email string) (EraseResult, error) {
	return s.erase(ctx, "email_hash", s.keys.HashEmail(email))
}

// EraseByID is EraseByEmail for a commenter known by id, including one a plugin
// vouches for. Unknown id: ErrNotFound.
func (s *Service) EraseByID(ctx context.Context, id uuid.UUID) (EraseResult, error) {
	return s.erase(ctx, "id", id)
}

func (s *Service) erase(ctx context.Context, where string, arg any) (EraseResult, error) {
	now := s.utcNow()
	var res EraseResult
	var deleted, anonymised []Comment
	err := s.db.WithTx(ctx, func(tx pgx.Tx) error {
		c, _, err := s.commenterRowBy(ctx, tx, where, arg)
		if err != nil {
			return err
		}
		// The IP block MarkSpam learned from this commenter's spam comments is
		// personal data and goes with them. A /24 hash is shared with everyone
		// on that network, so network blocks stay, as does any block that no
		// spam comment of this commenter could have taught.
		if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_comment_blocks WHERE source = 'spam' AND kind = 'ip_hash'
			AND value IN (SELECT ip_hash FROM hatamoto_comments WHERE commenter_id = $1 AND status = 'spam')`, c.ID); err != nil {
			return err
		}
		touched := map[uuid.UUID]bool{}
		// Delete leaves first; deleting a leaf can turn its parent into one.
		for {
			rows, err := tx.Query(ctx, `DELETE FROM hatamoto_comments c WHERE c.commenter_id = $1
				AND NOT EXISTS (SELECT 1 FROM hatamoto_comments r WHERE r.parent_id = c.id)
				RETURNING `+commentColumns, c.ID)
			if err != nil {
				return err
			}
			n := 0
			for rows.Next() {
				cm, err := scanComment(rows)
				if err != nil {
					rows.Close()
					return err
				}
				deleted = append(deleted, cm)
				touched[cm.EntityID] = true
				n++
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			if n == 0 {
				break
			}
			res.Deleted += n
		}
		rows, err := tx.Query(ctx, `UPDATE hatamoto_comments SET author_name = '', body_raw = $2, body_html = $2,
				has_links = false, link_domains = '{}', body_hash = '', ip_hash = '', net_hash = '',
				user_agent = '', notify_replies = false, updated_at = $3
			WHERE commenter_id = $1 RETURNING `+commentColumns, c.ID, deletedBody, now)
		if err != nil {
			return err
		}
		for rows.Next() {
			cm, err := scanComment(rows)
			if err != nil {
				rows.Close()
				return err
			}
			anonymised = append(anonymised, cm)
			touched[cm.EntityID] = true
			res.Anonymised++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		// Entity counts rows first (in id order), the commenter row last.
		entities := make([]uuid.UUID, 0, len(touched))
		for e := range touched {
			entities = append(entities, e)
		}
		slices.SortFunc(entities, func(x, y uuid.UUID) int { return bytes.Compare(x[:], y[:]) })
		for _, e := range entities {
			if err := lockAndRecount(ctx, tx, e, now); err != nil {
				return fmt.Errorf("recount: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_commenters WHERE id = $1`, c.ID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return EraseResult{}, err
	}
	// The commenter row is gone: announced rows must not link back to it.
	for i := range deleted {
		deleted[i].CommenterID = nil
	}
	s.announceErased(ctx, deleted)
	for _, c := range anonymised {
		c.CommenterID = nil
		// Status is unchanged; before == after tells the cache the content changed.
		s.obs.each("updated", func(o Observer) { o.OnUpdated(ctx, c, c) })
	}
	return res, nil
}
