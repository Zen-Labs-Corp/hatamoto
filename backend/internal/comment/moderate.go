package comment

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrNotOwned is returned when a batch names a comment that is not on the
// plugin's own content type (or does not exist). Nothing was changed.
var ErrNotOwned = errors.New("comment: not on the plugin's content type")

// Scope names the content type a plugin may moderate: its manifest name and
// the schema slug it declared. Ownership is checked against the schema's
// origin_plugin on every call.
type Scope struct{ Plugin, Schema string }

// RankItem is one rank write.
type RankItem struct {
	ID   uuid.UUID
	Rank float64
}

// scopeSQL counts how many of the ids sit on a content type the plugin created.
// Ownership is read here, inside the write transaction, on every call: never
// cache it and never trust the manifest's entity_schema alone (a plugin loaded
// from a development directory skips the install-time schema check, so its
// entity_schema may name a type it does not own). is_builtin is nullable.
const scopeSQL = `SELECT count(*) FROM hatamoto_comments c
	JOIN hatamoto_entities e ON e.id = c.entity_id
	JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
	WHERE c.id = ANY($1) AND s.slug = $2 AND s.origin_plugin = $3 AND s.is_builtin IS NOT TRUE`

func checkScope(ctx context.Context, tx pgx.Tx, sc Scope, ids []uuid.UUID) error {
	var n int
	if err := tx.QueryRow(ctx, scopeSQL, ids, sc.Schema, sc.Plugin).Scan(&n); err != nil {
		return err
	}
	if n < len(ids) {
		return ErrNotOwned
	}
	return nil
}

func distinctIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func sortedUUIDs(set map[uuid.UUID]bool) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// MarkDead moves the plugin's approved comments among ids to dead
// and returns how many moved. All ids must be on the plugin's own content type
// or nothing changes (ErrNotOwned). Observers run after commit: OnUpdated per
// moved comment with a WithBatch context, then one OnBatch.
func (s *Service) MarkDead(ctx context.Context, sc Scope, ids []uuid.UUID) (int, error) {
	return s.moveBatch(ctx, sc, ids, StatusDead)
}

// Restore moves dead comments among ids back to approved; see MarkDead.
func (s *Service) Restore(ctx context.Context, sc Scope, ids []uuid.UUID) (int, error) {
	return s.moveBatch(ctx, sc, ids, StatusApproved)
}

func (s *Service) moveBatch(ctx context.Context, sc Scope, ids []uuid.UUID, to Status) (int, error) {
	ids = distinctIDs(ids)
	if len(ids) == 0 {
		return 0, nil
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return 0, err
	}
	now := s.utcNow()
	before := map[uuid.UUID]Comment{}
	var moved []Comment
	err = s.db.WithTx(ctx, func(tx pgx.Tx) error {
		before, moved = map[uuid.UUID]Comment{}, nil
		if err := checkScope(ctx, tx, sc, ids); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+commentColumns+` FROM hatamoto_comments
			WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
		if err != nil {
			return err
		}
		var movable []uuid.UUID
		for rows.Next() {
			c, err := scanComment(rows)
			if err != nil {
				rows.Close()
				return err
			}
			if (to == StatusDead && c.Status == StatusApproved) ||
				(to == StatusApproved && c.Status == StatusDead) {
				before[c.ID] = c
				movable = append(movable, c.ID)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(movable) == 0 {
			return nil
		}
		var deadBy *string
		if to == StatusDead {
			deadBy = &sc.Plugin
		}
		rows, err = tx.Query(ctx, `UPDATE hatamoto_comments SET status = $2, updated_at = $3, dead_by = $4,
				approved_at = CASE WHEN $2::text = 'approved' THEN COALESCE(approved_at, $3) ELSE approved_at END
			WHERE id = ANY($1) RETURNING `+commentColumns, movable, string(to), now, deadBy)
		if err != nil {
			return err
		}
		for rows.Next() {
			c, err := scanComment(rows)
			if err != nil {
				rows.Close()
				return err
			}
			moved = append(moved, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		sort.Slice(moved, func(i, j int) bool { return moved[i].ID.String() < moved[j].ID.String() })
		entities, commenters := map[uuid.UUID]bool{}, map[uuid.UUID]bool{}
		for _, c := range moved {
			entities[c.EntityID] = true
			if c.CommenterID != nil && (before[c.ID].Status == StatusApproved || to == StatusApproved) {
				commenters[*c.CommenterID] = true
			}
		}
		for _, id := range sortedUUIDs(entities) {
			if err := lockAndRecount(ctx, tx, id, now); err != nil {
				return err
			}
		}
		for _, id := range sortedUUIDs(commenters) {
			if err := refreshCommenter(ctx, tx, id, set.Thresholds, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	s.announceBatch(ctx, moved, before)
	return len(moved), nil
}

// announceBatch fires OnUpdated per moved comment (batch context) and one OnBatch.
func (s *Service) announceBatch(ctx context.Context, moved []Comment, before map[uuid.UUID]Comment) {
	if len(moved) == 0 {
		return
	}
	bctx := WithBatch(ctx)
	entities := map[uuid.UUID]bool{}
	for _, c := range moved {
		entities[c.EntityID] = true
		s.obs.each("updated", func(o Observer) { o.OnUpdated(bctx, before[c.ID], c) })
	}
	s.fireBatch(ctx, sortedUUIDs(entities))
}

func (s *Service) fireBatch(ctx context.Context, entities []uuid.UUID) {
	s.obs.each("batch", func(o Observer) { o.OnBatch(ctx, entities) })
}

// SetRank stores the rank of each item and returns how many comments changed
// rank. The ids must be on the plugin's own content type (ErrNotOwned, nothing
// written). A rank write touches neither updated_at nor OnUpdated; observers get
// one OnBatch when anything changed. A repeated id keeps its last rank.
func (s *Service) SetRank(ctx context.Context, sc Scope, items []RankItem) (int, error) {
	last := make(map[uuid.UUID]float64, len(items))
	var ids []uuid.UUID
	for _, it := range items {
		if math.IsNaN(it.Rank) || math.IsInf(it.Rank, 0) {
			return 0, fmt.Errorf("comment: rank of %s is not a finite number", it.ID)
		}
		if _, ok := last[it.ID]; !ok {
			ids = append(ids, it.ID)
		}
		last[it.ID] = it.Rank
	}
	if len(ids) == 0 {
		return 0, nil
	}
	ranks := make([]float64, len(ids))
	for i, id := range ids {
		ranks[i] = last[id]
	}
	entities := map[uuid.UUID]bool{}
	changed := 0
	err := s.db.WithTx(ctx, func(tx pgx.Tx) error {
		clear(entities)
		changed = 0
		if err := checkScope(ctx, tx, sc, ids); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `UPDATE hatamoto_comments c SET rank = v.rank
			FROM unnest($1::uuid[], $2::float8[]) AS v(id, rank)
			WHERE c.id = v.id AND c.rank IS DISTINCT FROM v.rank RETURNING c.entity_id`, ids, ranks)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e uuid.UUID
			if err := rows.Scan(&e); err != nil {
				return err
			}
			entities[e] = true
			changed++
		}
		return rows.Err()
	})
	if err != nil {
		return 0, err
	}
	if changed > 0 {
		s.fireBatch(ctx, sortedUUIDs(entities))
	}
	return changed, nil
}

// SetViewerFlag stores a plugin's show-dead flag for one of its commenter
// keys: a row while true, no row while false.
func (s *Service) SetViewerFlag(ctx context.Context, plugin, commenterKey string, showDead bool) error {
	if !showDead {
		_, err := s.db.Pool().Exec(ctx, `DELETE FROM hatamoto_comment_viewer_flags WHERE plugin = $1 AND commenter_key = $2`,
			plugin, commenterKey)
		return err
	}
	_, err := s.db.Pool().Exec(ctx, `INSERT INTO hatamoto_comment_viewer_flags (plugin, commenter_key, show_dead, updated_at)
		VALUES ($1, $2, true, $3)
		ON CONFLICT (plugin, commenter_key) DO UPDATE SET show_dead = true, updated_at = EXCLUDED.updated_at`,
		plugin, commenterKey, s.utcNow())
	return err
}

// ShowDeadPlugins answers which of the plugins (plugin -> the viewer's key for
// that plugin) have a show-dead flag set for the viewer.
func (s *Service) ShowDeadPlugins(ctx context.Context, keys map[string]string) ([]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	plugins, ks := make([]string, 0, len(keys)), make([]string, 0, len(keys))
	for p, k := range keys {
		plugins, ks = append(plugins, p), append(ks, k)
	}
	return s.stringRows(ctx, `SELECT plugin FROM hatamoto_comment_viewer_flags
		WHERE show_dead AND (plugin, commenter_key) IN (SELECT * FROM unnest($1::text[], $2::text[]))
		ORDER BY plugin`, plugins, ks)
}

// FlaggedPlugins lists the plugins that have any show-dead flag set.
func (s *Service) FlaggedPlugins(ctx context.Context) ([]string, error) {
	return s.stringRows(ctx, `SELECT DISTINCT plugin FROM hatamoto_comment_viewer_flags WHERE show_dead ORDER BY plugin`)
}

func (s *Service) stringRows(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := s.db.Pool().Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
