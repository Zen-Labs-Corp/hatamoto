package agents

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/database"
)

// Repository is the Postgres Store.
type Repository struct {
	db *database.DB
}

// NewRepository creates the Postgres-backed store.
func NewRepository(db *database.DB) *Repository {
	return &Repository{db: db}
}

type dailyKey struct {
	day     time.Time
	agentID string
	path    string
	status  int
}

type dailyAgg struct {
	operator, purpose string
	entityID          *uuid.UUID
	hits, bytes       int64
}

// InsertHits writes the raw rows and folds them into the daily rollup in one
// transaction. The rollup never receives an IP.
func (r *Repository) InsertHits(ctx context.Context, hits []Hit) error {
	if len(hits) == 0 {
		return nil
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q, args := buildHitsInsert(hits)
	if _, err := tx.Exec(ctx, q, args...); err != nil {
		return fmt.Errorf("insert hits: %w", err)
	}
	q, args = buildDailyUpsert(aggregateDaily(hits))
	if _, err := tx.Exec(ctx, q, args...); err != nil {
		return fmt.Errorf("upsert daily: %w", err)
	}
	return tx.Commit(ctx)
}

const hitColumns = 14

func buildHitsInsert(hits []Hit) (string, []any) {
	var sb strings.Builder
	sb.WriteString(`INSERT INTO hatamoto_agent_hits
		(occurred_at, agent_id, operator, purpose, verification, ip, method, path, entity_id, status, bytes, duration_ms, source, ip_truncated) VALUES `)
	args := make([]any, 0, len(hits)*hitColumns)
	for i, h := range hits {
		if i > 0 {
			sb.WriteByte(',')
		}
		b := i * hitColumns
		fmt.Fprintf(&sb, "($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			b+1, b+2, b+3, b+4, b+5, b+6, b+7, b+8, b+9, b+10, b+11, b+12, b+13, b+14)
		var ip any
		if h.IP != nil {
			ip = h.IP.String()
		}
		var status any
		if h.Status != 0 {
			status = int16(h.Status)
		}
		args = append(args, h.At, h.AgentID, h.Operator, string(h.Purpose), h.Verification, ip,
			h.Method, h.Path, h.EntityID, status, h.Bytes, h.DurationMS, h.Source, false)
	}
	return sb.String(), args
}

func aggregateDaily(hits []Hit) map[dailyKey]*dailyAgg {
	m := make(map[dailyKey]*dailyAgg)
	for _, h := range hits {
		k := dailyKey{
			day:     time.Date(h.At.Year(), h.At.Month(), h.At.Day(), 0, 0, 0, 0, time.UTC),
			agentID: h.AgentID,
			path:    h.Path,
			status:  h.Status,
		}
		a := m[k]
		if a == nil {
			a = &dailyAgg{operator: h.Operator, purpose: string(h.Purpose)}
			m[k] = a
		}
		if a.entityID == nil {
			a.entityID = h.EntityID
		}
		a.hits++
		a.bytes += h.Bytes
	}
	return m
}

const dailyColumns = 9

// sortedDailyKeys orders keys (day, agent_id, path, status) bytewise, the same
// order verify's lock statement uses (COLLATE "C" on text), so the writer and
// the verify worker lock hatamoto_agent_daily rows in one global order.
func sortedDailyKeys(rows map[dailyKey]*dailyAgg) []dailyKey {
	keys := make([]dailyKey, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b dailyKey) int {
		return cmp.Or(a.day.Compare(b.day), cmp.Compare(a.agentID, b.agentID),
			cmp.Compare(a.path, b.path), cmp.Compare(a.status, b.status))
	})
	return keys
}

func buildDailyUpsert(rows map[dailyKey]*dailyAgg) (string, []any) {
	var sb strings.Builder
	sb.WriteString(`INSERT INTO hatamoto_agent_daily AS d (day, agent_id, path, status, operator, purpose, entity_id, hits, bytes_total) VALUES `)
	args := make([]any, 0, len(rows)*dailyColumns)
	for i, k := range sortedDailyKeys(rows) {
		a := rows[k]
		if i > 0 {
			sb.WriteByte(',')
		}
		b := i * dailyColumns
		fmt.Fprintf(&sb, "($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)", b+1, b+2, b+3, b+4, b+5, b+6, b+7, b+8, b+9)
		args = append(args, k.day, k.agentID, k.path, k.status, a.operator, a.purpose, a.entityID, a.hits, a.bytes)
	}
	sb.WriteString(` ON CONFLICT (day, agent_id, path, status) DO UPDATE SET
		hits = d.hits + EXCLUDED.hits,
		bytes_total = d.bytes_total + EXCLUDED.bytes_total,
		entity_id = COALESCE(d.entity_id, EXCLUDED.entity_id)`)
	return sb.String(), args
}
