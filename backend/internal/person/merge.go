package person

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/redirect"
)

// Merge errors.
var (
	// ErrMergeSame means keep and drop are the same person.
	ErrMergeSame = errors.New("a person cannot be merged into themselves")
	// ErrMergeBothLogins means both people belong to a user account: merging
	// would leave one of the accounts without a profile.
	ErrMergeBothLogins = errors.New("both people have a login")
)

// beginner is what a pool or a transaction offers to open a transaction (a
// savepoint inside one).
type beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// PathMover records that a public path moved (redirect.Service).
type PathMover interface {
	MoveEntityPaths(ctx context.Context, moves []redirect.PathMove) error
}

// MergeResult is what a merge changed, for the caller's message.
type MergeResult struct {
	Keep, Drop *Person
	// Posts is how many entities were credited to Drop and now credit Keep.
	Posts int
}

// Merge folds the person drop into keep, in one transaction: every entity
// credited to drop is credited to keep (including the author mirror and the
// plain-text byline), drop's login moves to keep when keep has none, keep's
// empty profile fields are filled from drop, and drop is deleted. Two people that both have a login are refused.
//
// Afterwards, when drop had a public page and keep has one too, drop's old
// path answers 301 to keep's. pathOf gives the public path of a slug; with
// redirects or pathOf nil no redirect is written. The redirect is written
// after the commit, so a failure there is returned with the result: the merge
// itself stands.
func (s *Store) Merge(ctx context.Context, keepID, dropID uuid.UUID, pathOf func(ctx context.Context, slug string) string, redirects PathMover) (*MergeResult, error) {
	if keepID == dropID {
		return nil, ErrMergeSame
	}
	b, ok := s.db.(beginner)
	if !ok {
		return nil, errors.New("merge needs a database that can open a transaction")
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rolled back unless committed

	in := &Store{db: tx}
	// Lock both rows in a fixed order so two merges cannot deadlock.
	first, second := keepID, dropID
	if second.String() < first.String() {
		first, second = second, first
	}
	for _, id := range []uuid.UUID{first, second} {
		var locked uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT e.id FROM hatamoto_entities e
			JOIN hatamoto_entity_schemas s ON s.id = e.schema_id AND s.slug = 'person'
			WHERE e.id = $1 FOR UPDATE OF e`, id).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
	}
	keep, err := in.Get(ctx, keepID)
	if err != nil {
		return nil, err
	}
	drop, err := in.Get(ctx, dropID)
	if err != nil {
		return nil, err
	}

	_, keepErr := in.LinkedUser(ctx, keepID)
	_, dropErr := in.LinkedUser(ctx, dropID)
	for _, e := range []error{keepErr, dropErr} {
		if e != nil && !errors.Is(e, ErrNotFound) {
			return nil, e
		}
	}
	hasKeep, hasDrop := keepErr == nil, dropErr == nil
	if hasKeep && hasDrop {
		return nil, ErrMergeBothLogins
	}
	if hasDrop {
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_user_people SET entity_id = $1 WHERE entity_id = $2`, keepID, dropID); err != nil {
			return nil, fmt.Errorf("move the login: %w", err)
		}
	}

	if err := fillProfile(ctx, tx, keepID, dropID); err != nil {
		return nil, fmt.Errorf("keep the profile: %w", err)
	}

	tag, err := tx.Exec(ctx, `UPDATE hatamoto_entities SET byline_person_id = $1 WHERE byline_person_id = $2`, keepID, dropID)
	if err != nil {
		return nil, fmt.Errorf("reassign the posts: %w", err)
	}
	// The author mirror and the plain-text byline follow keep.
	if err := NewByline(tx).PersonChanged(ctx, keepID); err != nil {
		return nil, fmt.Errorf("sync the byline: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, dropID); err != nil {
		return nil, fmt.Errorf("delete the merged person: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	res := &MergeResult{Keep: keep, Drop: drop, Posts: int(tag.RowsAffected())}
	if redirects != nil && pathOf != nil && drop.Public && keep.Public {
		from, to := pathOf(ctx, drop.Slug), pathOf(ctx, keep.Slug)
		if err := redirects.MoveEntityPaths(ctx, []redirect.PathMove{{From: from, To: to}}); err != nil {
			return res, fmt.Errorf("the people were merged, but the redirect from %s was not written: %w", from, err)
		}
	}
	return res, nil
}

// fillProfile copies every profile attribute keep lacks from drop (photo, job
// title, bio, links, ...), so a merge never loses what the dropped person had.
// What keep already has wins, and public and on_team stay keep's own decision.
// Nothing hidden is published by it: when keep is public and drop was hidden,
// drop's fields are not copied (they were never meant to be shown).
func fillProfile(ctx context.Context, tx pgx.Tx, keepID, dropID uuid.UUID) error {
	var keepRaw, dropRaw []byte
	if err := tx.QueryRow(ctx, `SELECT k.attributes, d.attributes FROM hatamoto_entities k, hatamoto_entities d WHERE k.id = $1 AND d.id = $2`,
		keepID, dropID).Scan(&keepRaw, &dropRaw); err != nil {
		return err
	}
	keep, drop := map[string]any{}, map[string]any{}
	if err := json.Unmarshal(keepRaw, &keep); err != nil {
		return err
	}
	if err := json.Unmarshal(dropRaw, &drop); err != nil {
		return err
	}
	if keep["public"] == true && drop["public"] != true {
		return nil
	}
	changed := false
	for k, v := range drop {
		if k == "public" || k == "on_team" || !hasValue(v) || hasValue(keep[k]) {
			continue
		}
		keep[k], changed = v, true
	}
	if !changed {
		return nil
	}
	out, err := json.Marshal(keep)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE hatamoto_entities SET attributes = $2::jsonb, updated_at = now() WHERE id = $1`, keepID, string(out))
	return err
}

// hasValue is false for an absent, empty or blank attribute value.
func hasValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(t) != ""
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	}
	return true
}

// WithRedirects makes MergePeople leave a 301 behind: pathOf gives the public
// path of a person slug (their type's URL pattern), mover records the move.
func (p *Profiles) WithRedirects(pathOf func(ctx context.Context, slug string) string, mover PathMover) *Profiles {
	p.pathOf, p.mover = pathOf, mover
	return p
}

// MergePeople is Store.Merge with the redirect settings of WithRedirects.
func (p *Profiles) MergePeople(ctx context.Context, keepID, dropID uuid.UUID) (*MergeResult, error) {
	return p.Store.Merge(ctx, keepID, dropID, p.pathOf, p.mover)
}
