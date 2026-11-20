package comment

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// BlockKind says what a blocklist value is. Only domains and hashes are ever
// stored: never a raw IP or email address.
type BlockKind string

// Blocklist kinds.
const (
	BlockEmailDomain BlockKind = "email_domain"
	BlockLinkDomain  BlockKind = "link_domain"
	BlockIPHash      BlockKind = "ip_hash"
	BlockNetHash     BlockKind = "net_hash"
	BlockBodyHash    BlockKind = "body_hash"
)

func (k BlockKind) valid() bool {
	switch k {
	case BlockEmailDomain, BlockLinkDomain, BlockIPHash, BlockNetHash, BlockBodyHash:
		return true
	}
	return false
}

func (k BlockKind) isDomain() bool { return k == BlockEmailDomain || k == BlockLinkDomain }

// Block is one blocklist row.
type Block struct {
	ID        uuid.UUID
	Kind      BlockKind
	Value     string
	Source    string // seed, manual or spam
	Note      string
	CreatedAt time.Time
}

// Probe is what a submission is matched against.
type Probe struct {
	EmailDomain string
	LinkDomains []string
	IPHash      string
	NetHash     string
	BodyHash    string
}

// normalizeDomain lowercases and strips spaces and a trailing dot.
func normalizeDomain(d string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
}

// domainCandidates lists a domain and its parent domains down to two labels, so
// a.b.example.test matches a block on example.test.
func domainCandidates(domain string) []string {
	d := normalizeDomain(domain)
	if d == "" {
		return nil
	}
	out := []string{d}
	for strings.Count(d, ".") > 1 {
		d = d[strings.Index(d, ".")+1:]
		out = append(out, d)
	}
	return out
}

const blockColumns = `id, kind, value, source, note, created_at`

func scanBlock(row pgx.Row) (Block, error) {
	var b Block
	var kind string
	err := row.Scan(&b.ID, &kind, &b.Value, &b.Source, &b.Note, &b.CreatedAt)
	b.Kind = BlockKind(kind)
	return b, err
}

func collectBlocks(rows pgx.Rows) ([]Block, error) {
	defer rows.Close()
	var out []Block
	for rows.Next() {
		b, err := scanBlock(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// MatchBlocks returns the blocklist rows a submission hits, in one query. Email
// and link domains also match their parent domains.
func (s *Service) MatchBlocks(ctx context.Context, p Probe) ([]Block, error) {
	emails := domainCandidates(p.EmailDomain)
	var links []string
	for _, d := range p.LinkDomains {
		links = append(links, domainCandidates(d)...)
	}
	rows, err := s.db.Pool().Query(ctx, `SELECT `+blockColumns+` FROM hatamoto_comment_blocks WHERE
			(kind = 'email_domain' AND value = ANY($1))
		 OR (kind = 'link_domain' AND value = ANY($2))
		 OR (kind = 'ip_hash' AND value = $3 AND $3 <> '')
		 OR (kind = 'net_hash' AND value = $4 AND $4 <> '')
		 OR (kind = 'body_hash' AND value = $5 AND $5 <> '')
		ORDER BY kind, value`,
		nonNil(emails), nonNil(links), p.IPHash, p.NetHash, p.BodyHash)
	if err != nil {
		return nil, err
	}
	return collectBlocks(rows)
}

// Blocks lists the rows of one kind, sorted by value.
func (s *Service) Blocks(ctx context.Context, kind BlockKind) ([]Block, error) {
	rows, err := s.db.Pool().Query(ctx, `SELECT `+blockColumns+` FROM hatamoto_comment_blocks
		WHERE kind = $1 ORDER BY value`, string(kind))
	if err != nil {
		return nil, err
	}
	return collectBlocks(rows)
}

// AddBlock adds a manual blocklist row. Domains are lowercased; a repeat is a no-op.
func (s *Service) AddBlock(ctx context.Context, kind BlockKind, value, note string, by *uuid.UUID) error {
	if !kind.valid() {
		return &FieldError{Field: "kind", Message: "unknown blocklist kind"}
	}
	value = strings.TrimSpace(value)
	if kind.isDomain() {
		value = normalizeDomain(value)
	}
	if value == "" {
		return &FieldError{Field: "value", Message: "must not be empty"}
	}
	_, err := s.db.Pool().Exec(ctx, `INSERT INTO hatamoto_comment_blocks (kind, value, source, note, created_at, created_by)
		VALUES ($1, $2, 'manual', $3, $4, $5) ON CONFLICT (kind, value) DO NOTHING`,
		string(kind), value, note, s.utcNow(), by)
	return err
}

// RemoveBlock deletes a blocklist row; an unknown id is ErrNotFound.
func (s *Service) RemoveBlock(ctx context.Context, id uuid.UUID) error {
	tag, err := s.db.Pool().Exec(ctx, `DELETE FROM hatamoto_comment_blocks WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MaxFormTokenID bounds a form token id; callers pass a hex SHA-256 (64 chars).
const MaxFormTokenID = 128

// ErrFormTokenID is returned for an empty or oversized form token id.
var ErrFormTokenID = errors.New("comment: form token id empty or too long")

// ConsumeFormToken records a single-use form token id until expiresAt. fresh is
// false when the token was already used or is already expired (nothing is
// written then). Issuing tokens is the caller's job.
func (s *Service) ConsumeFormToken(ctx context.Context, tokenID string, expiresAt time.Time) (bool, error) {
	if tokenID == "" || len(tokenID) > MaxFormTokenID {
		return false, ErrFormTokenID
	}
	if !expiresAt.After(s.utcNow()) {
		return false, nil
	}
	tag, err := s.db.Pool().Exec(ctx, `INSERT INTO hatamoto_comment_form_tokens (token_id, expires_at, used_at)
		VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, tokenID, expiresAt.UTC(), s.utcNow())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
