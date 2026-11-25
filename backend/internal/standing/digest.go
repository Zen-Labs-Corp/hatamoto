package standing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"hatamoto.dev/core/internal/schemafield"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DigestName is the key of the daily summary in hatamoto_digest_state.
const DigestName = "standing_digest"

// DigestRow is one automatic run in the daily summary.
type DigestRow struct {
	Plugin      string
	Run         string
	RunID       uuid.UUID
	ChangesetID int64
	Changes     int // applied updates and other non-create items
	Created     int // entries the run created on its own
	// Hosts counts, per host, the entries the run created that carry a valid
	// link to it in a link field of the approval. Nil when none did.
	Hosts map[string]int
	At    time.Time
}

// Digest lists the changesets published automatically since the given time,
// newest first, with how many updates each applied and how many new entries
// it created.
func (s *Store) Digest(ctx context.Context, since time.Time) ([]DigestRow, error) {
	rows, err := s.DB.Pool().Query(ctx, `
		SELECT c.plugin, r.name, r.id, c.id, c.published_at,
		       (SELECT count(*) FROM hatamoto_changeset_items i WHERE i.changeset_id = c.id AND i.result = 'applied'
		          AND NOT (i.kind = 'entity' AND i.op = 'upsert' AND i.before IS NULL AND i.before_token IS NULL)),
		       (SELECT count(*) FROM hatamoto_changeset_items i WHERE i.changeset_id = c.id AND i.result = 'applied'
		          AND i.kind = 'entity' AND i.op = 'upsert' AND i.before IS NULL AND i.before_token IS NULL)
		FROM hatamoto_changesets c JOIN hatamoto_runs r ON r.id = c.run_id
		WHERE c.auto_published AND c.state = 'published' AND c.published_at >= $1
		ORDER BY c.published_at DESC LIMIT 200`, since)
	if err != nil {
		return nil, fmt.Errorf("failed to read the daily summary: %w", err)
	}
	defer rows.Close()
	var out []DigestRow
	for rows.Next() {
		var d DigestRow
		if err := rows.Scan(&d.Plugin, &d.Run, &d.RunID, &d.ChangesetID, &d.At, &d.Changes, &d.Created); err != nil {
			return nil, fmt.Errorf("failed to read a summary row: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, s.addHosts(ctx, out)
}

// addHosts fills Hosts from the created entries' link fields. Only hosts of
// links that pass schemafield.LinkHost count, and an entry counts once per host.
func (s *Store) addHosts(ctx context.Context, out []DigestRow) error {
	var ids []int64
	at := map[int64]int{}
	for i, d := range out {
		if d.Created > 0 {
			ids = append(ids, d.ChangesetID)
			at[d.ChangesetID] = i
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.DB.Pool().Query(ctx, `
		SELECT i.changeset_id, i.payload->'attributes', a.policy->'create'->'link_fields'
		FROM hatamoto_changeset_items i
		JOIN hatamoto_changesets c ON c.id = i.changeset_id
		JOIN hatamoto_standing_approvals a ON a.id = c.approval_id
		WHERE i.changeset_id = ANY($1) AND i.result = 'applied'
		  AND i.kind = 'entity' AND i.op = 'upsert' AND i.before IS NULL AND i.before_token IS NULL
		  AND jsonb_typeof(a.policy->'create'->'link_fields') = 'array'`, ids)
	if err != nil {
		return fmt.Errorf("failed to read the linked hosts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cs int64
		var attrs map[string]any
		var keys []string
		if err := rows.Scan(&cs, &attrs, &keys); err != nil {
			return fmt.Errorf("failed to read a linked host row: %w", err)
		}
		seen := map[string]bool{}
		for _, k := range keys {
			raw, _ := attrs[k].(string)
			if h, ok := schemafield.LinkHost(raw); ok && !seen[h] {
				seen[h] = true
				d := &out[at[cs]]
				if d.Hosts == nil {
					d.Hosts = map[string]int{}
				}
				d.Hosts[h]++
			}
		}
	}
	return rows.Err()
}

// ClaimDigest records that the summary for the period ending at through is
// being sent, and reports whether this caller won the claim. Only one caller
// per period wins, across replicas and restarts.
func (s *Store) ClaimDigest(ctx context.Context, through time.Time) (bool, error) {
	var got time.Time
	err := s.DB.Pool().QueryRow(ctx, `
		INSERT INTO hatamoto_digest_state (name, sent_through) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET sent_through = EXCLUDED.sent_through
		WHERE hatamoto_digest_state.sent_through < EXCLUDED.sent_through
		RETURNING sent_through`, DigestName, through).Scan(&got)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("failed to claim the daily summary: %w", err)
	}
	return true, nil
}
