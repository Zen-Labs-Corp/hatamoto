package agents

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"hatamoto.dev/core/internal/database"
)

const (
	// verifyWindow bounds how far back pending hits are looked at.
	verifyWindow = "7 days"
	// PendingBatch is the maximum number of (agent, ip) pairs per run.
	PendingBatch = 500
)

// pendingPairsSQL lists distinct (agent, ip) pairs with pending hits, oldest
// first so a backlog cannot starve old pairs. A NULL ip is a valid pair (the
// verifier marks it unverifiable).
const pendingPairsSQL = `
SELECT agent_id, host(ip), min(occurred_at)
FROM hatamoto_agent_hits
WHERE verification = 'pending' AND agent_id IS NOT NULL
  AND occurred_at > now() - interval '` + verifyWindow + `'
GROUP BY 1, 2
ORDER BY 3
LIMIT $1`

// lockDailySQL row-locks the daily rows the apply statement will update, in the
// writer's order (day, agent_id, path, status; text bytewise, hence COLLATE "C"),
// so verify and the writer can never wait on each other in opposite order. It
// selects the same hits as applyVerificationSQL ($1 agent, $2 ip, $3 cutoff) and
// runs first, in the same transaction.
const lockDailySQL = `
SELECT 1 FROM hatamoto_agent_daily d
JOIN (
    SELECT DISTINCT (occurred_at AT TIME ZONE 'UTC')::date AS day, path, COALESCE(status, 0) AS status
    FROM hatamoto_agent_hits
    WHERE agent_id = $1 AND ip IS NOT DISTINCT FROM $2::inet AND verification = 'pending'
      AND occurred_at > now() - interval '` + verifyWindow + `'
      AND ($3::timestamptz IS NULL OR occurred_at < $3::timestamptz)
) k ON d.day = k.day AND d.path = k.path AND d.status = k.status
WHERE d.agent_id = $1
ORDER BY d.day, d.agent_id COLLATE "C", d.path COLLATE "C", d.status
FOR UPDATE OF d`

// applyVerificationSQL flips the pending hits of one pair and, in the same
// statement, moves the rollup counters by exactly the rows that flipped (the
// rows must still be pending, so a repeated run changes nothing). $1 agent,
// $2 ip (NULL allowed), $3 new state, $4 optional cutoff: only hits strictly
// older than it flip. The day is the UTC day, matching the writer's rollup key.
// Returns the number of hits flipped.
const applyVerificationSQL = `
WITH u AS (
    UPDATE hatamoto_agent_hits
    SET verification = $3::text
    WHERE agent_id = $1 AND ip IS NOT DISTINCT FROM $2::inet AND verification = 'pending'
      AND occurred_at > now() - interval '` + verifyWindow + `'
      AND ($4::timestamptz IS NULL OR occurred_at < $4::timestamptz)
    RETURNING (occurred_at AT TIME ZONE 'UTC')::date AS day, path, COALESCE(status, 0) AS status
), c AS (
    SELECT day, path, status, count(*) AS n FROM u GROUP BY 1, 2, 3
), d AS (
    UPDATE hatamoto_agent_daily d
    SET verified_hits     = d.verified_hits     + CASE WHEN $3::text = 'verified'     THEN c.n ELSE 0 END,
        spoofed_hits      = d.spoofed_hits      + CASE WHEN $3::text = 'spoofed'      THEN c.n ELSE 0 END,
        unverifiable_hits = d.unverifiable_hits + CASE WHEN $3::text = 'unverifiable' THEN c.n ELSE 0 END
    FROM c
    WHERE d.day = c.day AND d.agent_id = $1 AND d.path = c.path AND d.status = c.status
    RETURNING 1
)
SELECT (SELECT count(*) FROM u)`

// lockStaleDailySQL and sweepStaleSQL retire pending hits that fell out of the
// verify window (older than verifyWindow, e.g. after the worker was off for a
// week): they become unverifiable so pending is never terminal and the rollup
// adds up. Same shape and lock order as the per-pair statements, over all
// agents; the day rows locked are only those the sweep will update.
const lockStaleDailySQL = `
SELECT 1 FROM hatamoto_agent_daily d
JOIN (
    SELECT DISTINCT agent_id, (occurred_at AT TIME ZONE 'UTC')::date AS day, path, COALESCE(status, 0) AS status
    FROM hatamoto_agent_hits
    WHERE verification = 'pending' AND agent_id IS NOT NULL
      AND occurred_at <= now() - interval '` + verifyWindow + `'
) k ON d.day = k.day AND d.agent_id = k.agent_id AND d.path = k.path AND d.status = k.status
ORDER BY d.day, d.agent_id COLLATE "C", d.path COLLATE "C", d.status
FOR UPDATE OF d`

const sweepStaleSQL = `
WITH u AS (
    UPDATE hatamoto_agent_hits
    SET verification = 'unverifiable'
    WHERE verification = 'pending' AND agent_id IS NOT NULL
      AND occurred_at <= now() - interval '` + verifyWindow + `'
    RETURNING agent_id, (occurred_at AT TIME ZONE 'UTC')::date AS day, path, COALESCE(status, 0) AS status
), c AS (
    SELECT agent_id, day, path, status, count(*) AS n FROM u GROUP BY 1, 2, 3, 4
), d AS (
    UPDATE hatamoto_agent_daily d
    SET unverifiable_hits = d.unverifiable_hits + c.n
    FROM c
    WHERE d.day = c.day AND d.agent_id = c.agent_id AND d.path = c.path AND d.status = c.status
    RETURNING 1
)
SELECT (SELECT count(*) FROM u)`

// PendingPair is one (agent, ip) group of pending hits. IP is the zero Addr
// when the hits carry no address.
type PendingPair struct {
	AgentID  string
	IP       netip.Addr
	OldestAt time.Time
}

// VerifyRepo is the storage the verify worker needs.
type VerifyRepo interface {
	PendingPairs(ctx context.Context, limit int) ([]PendingPair, error)
	// ApplyVerification flips the pair's pending hits (older than before, when
	// non-zero) to state and moves the rollup counters. Returns hits flipped.
	ApplyVerification(ctx context.Context, p PendingPair, state State, before time.Time) (int64, error)
	// SweepStalePending flips pending hits older than the verify window to
	// unverifiable and moves the rollup. Returns hits flipped.
	SweepStalePending(ctx context.Context) (int64, error)
}

// VerifyRepository is the Postgres VerifyRepo.
type VerifyRepository struct {
	db *database.DB
}

func NewVerifyRepository(db *database.DB) *VerifyRepository { return &VerifyRepository{db: db} }

func (r *VerifyRepository) PendingPairs(ctx context.Context, limit int) ([]PendingPair, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, pendingPairsSQL, limit)
	if err != nil {
		return nil, fmt.Errorf("pending pairs: %w", err)
	}
	defer rows.Close()
	var out []PendingPair
	for rows.Next() {
		var p PendingPair
		var ip *string
		if err := rows.Scan(&p.AgentID, &ip, &p.OldestAt); err != nil {
			return nil, fmt.Errorf("scan pending pair: %w", err)
		}
		if ip != nil {
			if a, err := netip.ParseAddr(*ip); err == nil {
				p.IP = a.Unmap()
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *VerifyRepository) ApplyVerification(ctx context.Context, p PendingPair, state State, before time.Time) (int64, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	var ip, cutoff any
	if p.IP.IsValid() {
		ip = p.IP.String()
	}
	if !before.IsZero() {
		cutoff = before
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, lockDailySQL, p.AgentID, ip, cutoff); err != nil {
		return 0, fmt.Errorf("lock daily rows: %w", err)
	}
	var n int64
	if err := tx.QueryRow(ctx, applyVerificationSQL, p.AgentID, ip, string(state), cutoff).Scan(&n); err != nil {
		return 0, fmt.Errorf("apply verification: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return n, nil
}

func (r *VerifyRepository) SweepStalePending(ctx context.Context) (int64, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, lockStaleDailySQL); err != nil {
		return 0, fmt.Errorf("lock daily rows: %w", err)
	}
	var n int64
	if err := tx.QueryRow(ctx, sweepStaleSQL).Scan(&n); err != nil {
		return 0, fmt.Errorf("sweep stale pending: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return n, nil
}
