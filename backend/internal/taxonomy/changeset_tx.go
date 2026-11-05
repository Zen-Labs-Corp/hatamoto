package taxonomy

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/jsonx"
)

// ErrTermConflict is returned by ApplyChangesetTx when the live term moved
// since the changeset item was staged (its updated_at differs from the base).
var ErrTermConflict = errors.New("taxonomy: term changed since it was staged")

// maxSlugAttempts bounds the "-2", "-3", ... probe for a free slug.
const maxSlugAttempts = 100

// TermOp is one term write of a changeset, applied inside the caller's
// transaction.
//
// Without ID the op creates a term of Kind. With ID it updates that term: Name
// (empty keeps it), Slug (empty keeps it), Description and ParentID replace
// the stored values (nil ParentID makes the term a root), SEO replaces them
// when non-nil. Kind and Locale are fixed at creation. Delete removes the
// term; its children become roots (FK SET NULL) and its entity attachments
// are cascaded away, exactly like DeleteTerm.
//
// BaseUpdatedAt, when set, is the updated_at the item was staged against: a
// term that moved since is a conflict.
type TermOp struct {
	ID            *uuid.UUID
	Kind          string
	Slug          string
	Name          string
	Description   string
	ParentID      *uuid.UUID
	Locale        string
	SEO           jsontext.Value
	BaseUpdatedAt *time.Time
	// Origin owns a created term; empty is "owner". Updates keep the stored origin.
	Origin string
	Delete bool
}

// TermResult reports what ApplyChangesetTx did.
type TermResult struct {
	ID      uuid.UUID
	Slug    string
	Created bool
	Deleted bool
	// Conflict is true together with ErrTermConflict.
	Conflict bool
	// UpdatedAt is the stamp of the written row, the next base token.
	UpdatedAt time.Time
	// Before is the pre-image (JSON) of an updated or deleted term, nil for a
	// create. RestoreChangesetTx takes it back.
	Before jsontext.Value
}

// termSnapshot is the stored pre-image: every column a changeset can change.
type termSnapshot struct {
	ID               uuid.UUID      `json:"id"`
	Kind             string         `json:"kind"`
	Slug             string         `json:"slug"`
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	ParentID         *uuid.UUID     `json:"parent_id,omitempty"`
	Locale           string         `json:"locale"`
	TranslationGroup *uuid.UUID     `json:"translation_group,omitempty"`
	SEO              jsontext.Value `json:"seo"`
	Origin           string         `json:"origin"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

const snapshotCols = `id, kind, slug, name, description, parent_id, locale, translation_group, seo, origin, created_at, updated_at`

func (s *termSnapshot) dest() []any {
	return []any{&s.ID, &s.Kind, &s.Slug, &s.Name, &s.Description, &s.ParentID, &s.Locale,
		&s.TranslationGroup, &s.SEO, &s.Origin, &s.CreatedAt, &s.UpdatedAt}
}

// ApplyChangesetTx applies op on tx. It runs its writes in a savepoint, so a
// failed op leaves tx usable. It emits nothing: call NotifyChanged once the
// transaction committed. Ownership is not checked here; the engine does that.
func ApplyChangesetTx(ctx context.Context, tx pgx.Tx, op TermOp) (TermResult, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return TermResult{}, fmt.Errorf("term changeset savepoint: %w", err)
	}
	res, err := applyTerm(ctx, sp, op)
	if err != nil {
		_ = sp.Rollback(ctx)
		return res, err
	}
	if err := sp.Commit(ctx); err != nil {
		return TermResult{}, fmt.Errorf("term changeset release: %w", err)
	}
	return res, nil
}

func applyTerm(ctx context.Context, tx pgx.Tx, op TermOp) (TermResult, error) {
	if op.ID == nil {
		if op.Delete {
			return TermResult{}, ErrTermNotFound
		}
		return createTermTx(ctx, tx, op)
	}
	var cur termSnapshot
	err := tx.QueryRow(ctx, `SELECT `+snapshotCols+` FROM hatamoto_terms WHERE id = $1 FOR UPDATE`, *op.ID).Scan(cur.dest()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return TermResult{}, ErrTermNotFound
	}
	if err != nil {
		return TermResult{}, fmt.Errorf("load term: %w", err)
	}
	if op.BaseUpdatedAt != nil && !op.BaseUpdatedAt.Truncate(time.Microsecond).Equal(cur.UpdatedAt.Truncate(time.Microsecond)) {
		return TermResult{ID: cur.ID, Slug: cur.Slug, Conflict: true}, ErrTermConflict
	}
	before, err := jsonx.Marshal(cur)
	if err != nil {
		return TermResult{}, fmt.Errorf("term pre-image: %w", err)
	}
	if op.Delete {
		if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_terms WHERE id = $1`, cur.ID); err != nil {
			return TermResult{}, fmt.Errorf("delete term: %w", err)
		}
		return TermResult{ID: cur.ID, Slug: cur.Slug, Deleted: true, Before: before}, nil
	}
	res, err := updateTermTx(ctx, tx, cur, op)
	res.Before = before
	return res, err
}

func createTermTx(ctx context.Context, tx pgx.Tx, op TermOp) (TermResult, error) {
	kind := strings.ToLower(strings.TrimSpace(op.Kind))
	name := strings.TrimSpace(op.Name)
	slug := strings.ToLower(strings.TrimSpace(op.Slug))
	if !ValidKind(kind) {
		return TermResult{}, ErrInvalidKind
	}
	if name == "" || len(name) > MaxNameLen {
		return TermResult{}, ErrInvalidName
	}
	if slug == "" {
		slug = Slugify(name)
	}
	if !ValidSlug(slug) {
		return TermResult{}, ErrInvalidSlug
	}
	desc := strings.TrimSpace(op.Description)
	if len(desc) > MaxDescriptionLen {
		return TermResult{}, ErrInvalidDescription
	}
	locale, err := cleanLocale(op.Locale)
	if err != nil {
		return TermResult{}, err
	}
	seo, err := txSEO(op.SEO, "")
	if err != nil {
		return TermResult{}, err
	}
	if op.ParentID != nil {
		if err := checkParentTx(ctx, tx, kind, uuid.Nil, *op.ParentID); err != nil {
			return TermResult{}, err
		}
	}
	origin := op.Origin
	if origin == "" {
		origin = "owner"
	}
	id := uuid.New()
	var updated time.Time
	got, err := withFreeSlug(ctx, tx, kind, slug, uuid.Nil, func(tx pgx.Tx, candidate string) error {
		return tx.QueryRow(ctx, `
            INSERT INTO hatamoto_terms (id, kind, slug, name, description, parent_id, locale, seo, origin, created_at, updated_at)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), now())
            RETURNING updated_at`,
			id, kind, candidate, name, desc, op.ParentID, locale, seo, origin).Scan(&updated)
	})
	if err != nil {
		return TermResult{}, err
	}
	return TermResult{ID: id, Slug: got, Created: true, UpdatedAt: updated}, nil
}

func updateTermTx(ctx context.Context, tx pgx.Tx, cur termSnapshot, op TermOp) (TermResult, error) {
	name := strings.TrimSpace(op.Name)
	if name == "" {
		name = cur.Name
	}
	if len(name) > MaxNameLen {
		return TermResult{}, ErrInvalidName
	}
	slug := strings.ToLower(strings.TrimSpace(op.Slug))
	if slug == "" {
		slug = cur.Slug
	}
	if !ValidSlug(slug) {
		return TermResult{}, ErrInvalidSlug
	}
	desc := strings.TrimSpace(op.Description)
	if len(desc) > MaxDescriptionLen {
		return TermResult{}, ErrInvalidDescription
	}
	seo := []byte(cur.SEO)
	if op.SEO != nil {
		var stored string
		var old TermSEO
		if jsonx.Unmarshal(cur.SEO, &old, jsonx.StoredLoose) == nil {
			stored = old.CanonicalURL
		}
		var err error
		if seo, err = txSEO(op.SEO, stored); err != nil {
			return TermResult{}, err
		}
	}
	if op.ParentID != nil {
		if err := checkParentTx(ctx, tx, cur.Kind, cur.ID, *op.ParentID); err != nil {
			return TermResult{}, err
		}
		height, err := subtreeHeightTx(ctx, tx, cur.ID)
		if err != nil {
			return TermResult{}, err
		}
		depth, err := chainLenTx(ctx, tx, *op.ParentID)
		if err != nil {
			return TermResult{}, err
		}
		// The whole subtree moves with the term: it must still fit.
		if depth+1+height > MaxDepth {
			return TermResult{}, fmt.Errorf("%w: nesting deeper than %d", ErrTermParentInvalid, MaxDepth)
		}
	}
	var updated time.Time
	write := func(tx pgx.Tx, candidate string) error {
		return tx.QueryRow(ctx, `
            UPDATE hatamoto_terms
               SET slug = $2, name = $3, description = $4, parent_id = $5, seo = $6, updated_at = now()
             WHERE id = $1
         RETURNING updated_at`,
			cur.ID, candidate, name, desc, op.ParentID, seo).Scan(&updated)
	}
	got := slug
	var err error
	if slug == cur.Slug {
		err = write(tx, slug)
	} else {
		got, err = withFreeSlug(ctx, tx, cur.Kind, slug, cur.ID, write)
	}
	if err != nil {
		return TermResult{}, err
	}
	return TermResult{ID: cur.ID, Slug: got, UpdatedAt: updated}, nil
}

// txSEO validates a raw SEO document like Service.UpdateTerm does and returns
// the jsonb text to store ("{}" when empty).
func txSEO(raw jsontext.Value, stored string) ([]byte, error) {
	if raw == nil {
		return []byte("{}"), nil
	}
	var in TermSEO
	if err := jsonx.Unmarshal(raw, &in, jsonx.StoredLoose); err != nil {
		return nil, ErrInvalidSEO
	}
	v, err := validateTermSEO(in, stored)
	if err != nil {
		return nil, err
	}
	return encodeTermSEO(v)
}

// suffixedSlug is slug with "-n" appended (n >= 2), the base cut so the result
// fits MaxSlugLen and still ends on a letter or digit.
func suffixedSlug(slug string, n int) string {
	if n <= 1 {
		return slug
	}
	suffix := "-" + strconv.Itoa(n)
	if len(slug)+len(suffix) > MaxSlugLen {
		slug = strings.TrimRight(slug[:MaxSlugLen-len(suffix)], "-")
	}
	return slug + suffix
}

// withFreeSlug calls write with slug, then slug-2, slug-3, ... until one is
// free for kind (self, when not Nil, is the term being renamed). A free slug is
// probed with SELECT first; a race that still hits the unique index is retried
// in a savepoint, which keeps tx usable.
func withFreeSlug(ctx context.Context, tx pgx.Tx, kind, slug string, self uuid.UUID, write func(tx pgx.Tx, slug string) error) (string, error) {
	for n := 1; n <= maxSlugAttempts; n++ {
		candidate := suffixedSlug(slug, n)
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_terms WHERE kind = $1 AND slug = $2 AND id <> $3)`,
			kind, candidate, self).Scan(&taken); err != nil {
			return "", fmt.Errorf("check term slug: %w", err)
		}
		if taken {
			continue
		}
		sp, err := tx.Begin(ctx)
		if err != nil {
			return "", fmt.Errorf("term slug savepoint: %w", err)
		}
		err = write(sp, candidate)
		if err == nil {
			if err := sp.Commit(ctx); err != nil {
				return "", fmt.Errorf("term slug release: %w", err)
			}
			return candidate, nil
		}
		_ = sp.Rollback(ctx)
		if uniqueViolation(err) {
			continue
		}
		if foreignKeyViolation(err) {
			return "", ErrTermParentNotFound
		}
		return "", fmt.Errorf("write term: %w", err)
	}
	return "", ErrTermSlugConflict
}

// chainTx returns the chain from id upwards (id first, then its parents), at
// most MaxDepth+1 long; nil when id does not exist.
func chainTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) ([]termSnapshot, error) {
	var chain []termSnapshot
	next := &id
	for next != nil && len(chain) <= MaxDepth {
		var t termSnapshot
		err := tx.QueryRow(ctx, `SELECT id, kind, parent_id FROM hatamoto_terms WHERE id = $1`, *next).Scan(&t.ID, &t.Kind, &t.ParentID)
		if errors.Is(err, pgx.ErrNoRows) {
			if len(chain) == 0 {
				return nil, nil
			}
			break
		}
		if err != nil {
			return nil, fmt.Errorf("term chain: %w", err)
		}
		chain = append(chain, t)
		next = t.ParentID
	}
	return chain, nil
}

func chainLenTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (int, error) {
	chain, err := chainTx(ctx, tx, id)
	return len(chain), err
}

// checkParentTx is Service.checkParent on tx: the parent exists, has the same
// kind, makes no cycle through self (Nil for a new term) and fits MaxDepth.
func checkParentTx(ctx context.Context, tx pgx.Tx, kind string, self, parent uuid.UUID) error {
	if self != uuid.Nil && self == parent {
		return ErrTermParentInvalid
	}
	chain, err := chainTx(ctx, tx, parent)
	if err != nil {
		return err
	}
	if len(chain) == 0 {
		return ErrTermParentNotFound
	}
	if chain[0].Kind != kind {
		return fmt.Errorf("%w: parent is a %s, not a %s", ErrTermParentInvalid, chain[0].Kind, kind)
	}
	for _, t := range chain {
		if self != uuid.Nil && t.ID == self {
			return fmt.Errorf("%w: cycle", ErrTermParentInvalid)
		}
	}
	if len(chain)+1 > MaxDepth {
		return fmt.Errorf("%w: nesting deeper than %d", ErrTermParentInvalid, MaxDepth)
	}
	return nil
}

// subtreeHeightTx is how many levels of descendants id has (0 for a leaf).
func subtreeHeightTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (int, error) {
	var h int
	err := tx.QueryRow(ctx, `
        WITH RECURSIVE d(id, h) AS (
            SELECT id, 1 FROM hatamoto_terms WHERE parent_id = $1
            UNION ALL
            SELECT t.id, d.h + 1 FROM hatamoto_terms t JOIN d ON t.parent_id = d.id WHERE d.h <= $2
        ) SELECT COALESCE(max(h), 0) FROM d`, id, MaxDepth*2).Scan(&h)
	if err != nil {
		return 0, fmt.Errorf("term subtree: %w", err)
	}
	return h, nil
}

// RestoreChangesetTx puts a term back to the pre-image ApplyChangesetTx
// returned: the row is rewritten when it still exists and re-inserted when it
// was deleted. A parent that is gone by now becomes no parent. Children that a
// delete orphaned are not re-attached. Reverting a create is a Delete op.
func RestoreChangesetTx(ctx context.Context, tx pgx.Tx, before jsontext.Value) error {
	var s termSnapshot
	if err := jsonx.Unmarshal(before, &s, jsonx.StoredLoose); err != nil || s.ID == uuid.Nil {
		return fmt.Errorf("term pre-image: unreadable")
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("term restore savepoint: %w", err)
	}
	if s.ParentID != nil {
		var ok bool
		if err := sp.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_terms WHERE id = $1)`, *s.ParentID).Scan(&ok); err != nil {
			_ = sp.Rollback(ctx)
			return fmt.Errorf("term restore parent: %w", err)
		}
		if !ok {
			s.ParentID = nil
		}
	}
	_, err = sp.Exec(ctx, `
        INSERT INTO hatamoto_terms (id, kind, slug, name, description, parent_id, locale, translation_group, seo, origin, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
        ON CONFLICT (id) DO UPDATE SET
            slug = EXCLUDED.slug, name = EXCLUDED.name, description = EXCLUDED.description,
            parent_id = EXCLUDED.parent_id, seo = EXCLUDED.seo, updated_at = now()`,
		s.ID, s.Kind, s.Slug, s.Name, s.Description, s.ParentID, s.Locale, s.TranslationGroup, []byte(s.SEO), s.Origin, s.CreatedAt)
	if err != nil {
		_ = sp.Rollback(ctx)
		if uniqueViolation(err) {
			return ErrTermSlugConflict
		}
		return fmt.Errorf("restore term: %w", err)
	}
	return sp.Commit(ctx)
}

// NotifyChanged runs the change observer once, for use after the transaction
// of a changeset committed. It does nothing for a nil Service or observer.
func (s *Service) NotifyChanged(ctx context.Context) {
	if s != nil {
		s.notifyChanged(ctx)
	}
}
