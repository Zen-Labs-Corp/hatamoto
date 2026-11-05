package changeset

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/jsonx"
)

const (
	// classifyBatch is how many unclassified items are read at a time, so a
	// large changeset never holds all its payloads at once.
	classifyBatch = 500
	// siteHostRows caps the published entities read to learn the hosts the site links to.
	siteHostRows = 5000
)

// Classify classes every item of a changeset that has no risk yet and saves it,
// in its own transaction under the changeset lock. It is idempotent: classed
// items are left alone. The review reads need it done first.
func (e *Engine) Classify(ctx context.Context, csID int64) error {
	return e.DB.WithTx(ctx, func(tx pgx.Tx) error {
		return e.EnsureClassified(ctx, tx, csID)
	})
}

// EnsureClassified is Classify inside the caller's transaction, which must
// hold the changeset's row lock.
func (e *Engine) EnsureClassified(ctx context.Context, tx pgx.Tx, csID int64) error {
	var plugin string
	err := tx.QueryRow(ctx, `SELECT plugin FROM hatamoto_changesets WHERE id = $1`, csID).Scan(&plugin)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to read changeset: %w", err)
	}
	return e.ensureClassified(ctx, tx, csID, plugin)
}

func (e *Engine) ensureClassified(ctx context.Context, tx pgx.Tx, csID int64, plugin string) error {
	var pending bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_changeset_items WHERE changeset_id = $1 AND risk IS NULL)`, csID).Scan(&pending); err != nil {
		return fmt.Errorf("failed to read items: %w", err)
	}
	if !pending {
		return nil
	}
	env, err := loadEnv(ctx, tx, csID)
	if err != nil {
		return err
	}
	origin := Origin(plugin)
	last := 0
	for {
		batch, err := loadUnclassified(ctx, tx, csID, last)
		if err != nil {
			return err
		}
		for _, it := range batch {
			live, err := loadLive(ctx, tx, origin, it)
			if err != nil {
				return fmt.Errorf("item %d (%s): %w", it.Seq, it.Kind, err)
			}
			v := Classify(it.Item, live, env)
			if _, err := tx.Exec(ctx, `
				UPDATE hatamoto_changeset_items SET risk = $2, risk_reason = NULLIF($3, ''), risk_ref = NULLIF($4, 0) WHERE id = $1`,
				it.ID, string(v.Risk), string(v.Reason), v.Ref); err != nil {
				return fmt.Errorf("failed to save risk: %w", err)
			}
			last = it.Seq
		}
		if len(batch) < classifyBatch {
			return nil
		}
	}
}

func loadUnclassified(ctx context.Context, tx pgx.Tx, csID int64, afterSeq int) ([]StoredItem, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, seq, kind, op, COALESCE(source_key, ''), COALESCE(target_id, ''), payload
		FROM hatamoto_changeset_items WHERE changeset_id = $1 AND risk IS NULL AND seq > $2 ORDER BY seq LIMIT $3`,
		csID, afterSeq, classifyBatch)
	if err != nil {
		return nil, fmt.Errorf("failed to read items: %w", err)
	}
	defer rows.Close()
	var out []StoredItem
	for rows.Next() {
		var it StoredItem
		var kind string
		var payload []byte
		if err := rows.Scan(&it.ID, &it.Seq, &kind, &it.Op, &it.SourceKey, &it.TargetID, &payload); err != nil {
			return nil, err
		}
		it.Kind, it.Payload = Kind(kind), jsontext.Value(payload)
		out = append(out, it)
	}
	return out, rows.Err()
}

// loadLive reads the entity an item updates, or nil for a create and for the
// kinds the classifier needs no live state of.
func loadLive(ctx context.Context, tx rowQuerier, origin string, it StoredItem) (*Live, error) {
	if it.Kind != KindEntity {
		return nil, nil
	}
	target := it.TargetID
	if target == "" {
		var err error
		if target, err = mappedTarget(ctx, tx, origin, it.Kind, it.SourceKey); err != nil {
			return nil, err
		}
	}
	id, err := uuid.Parse(target)
	if err != nil {
		return nil, nil
	}
	var l Live
	var parent *string
	var attrs []byte
	err = tx.QueryRow(ctx, `SELECT slug, parent_id::text, attributes FROM hatamoto_entities WHERE id = $1`, id).Scan(&l.Slug, &parent, &attrs)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read live entity: %w", err)
	}
	if parent != nil {
		l.ParentID = *parent
	}
	l.Attributes = jsontext.Value(attrs)
	return &l, nil
}

// loadEnv builds the site context: hosts the published entities link to, and
// the paths of the entity items this changeset stages.
func loadEnv(ctx context.Context, tx pgx.Tx, csID int64) (Env, error) {
	env := Env{SiteHosts: map[string]bool{}, StagedPaths: map[string]int{}}
	rows, err := tx.Query(ctx, `SELECT attributes FROM hatamoto_entities WHERE status = 'published' ORDER BY updated_at DESC LIMIT $1`, siteHostRows)
	if err != nil {
		return env, fmt.Errorf("failed to read site links: %w", err)
	}
	for rows.Next() {
		var attrs []byte
		if err := rows.Scan(&attrs); err != nil {
			rows.Close()
			return env, err
		}
		var v any
		if jsonx.Unmarshal(attrs, &v, jsonx.StoredLoose) != nil {
			continue
		}
		for _, l := range scanStrings(v).links {
			if l.host != "" {
				env.SiteHosts[l.host] = true
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return env, err
	}
	rows, err = tx.Query(ctx, `
		SELECT seq, payload->>'slug' FROM hatamoto_changeset_items
		WHERE changeset_id = $1 AND kind = $2 AND op = $3 AND COALESCE(payload->>'slug', '') <> ''`, csID, string(KindEntity), OpUpsert)
	if err != nil {
		return env, fmt.Errorf("failed to read staged paths: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var slug string
		if err := rows.Scan(&seq, &slug); err != nil {
			return env, err
		}
		env.StagedPaths["/"+slug] = seq
	}
	return env, rows.Err()
}
