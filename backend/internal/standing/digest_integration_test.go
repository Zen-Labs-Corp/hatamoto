//go:build integration

package standing

import (
	"testing"
	"time"
)

func TestDigestListsOnlyTheLastDayOfAutomaticRuns(t *testing.T) {
	s, ctx := newStore(t)
	p := testPlugin(t, s, ctx)
	now := time.Now().UTC()
	recent, recentCS := insertRun(t, s, p, "cron", "published", true, now.Add(-2*time.Hour))
	insertRun(t, s, p, "cron", "published", true, now.Add(-30*time.Hour))
	insertRun(t, s, p, "owner", "published", false, now.Add(-time.Hour))
	insertRun(t, s, p, "cron", "staged", false, now.Add(-time.Hour))
	got, err := s.Digest(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var mine []DigestRow
	for _, d := range got {
		if d.Plugin == p {
			mine = append(mine, d)
		}
	}
	if len(mine) != 1 || mine[0].RunID != recent || mine[0].ChangesetID != recentCS || mine[0].Run != "prices" {
		t.Fatalf("Digest = %+v, want only the recent automatic run", mine)
	}
}

func TestClaimDigestHasOneWinnerPerPeriod(t *testing.T) {
	s, ctx := newStore(t)
	t.Cleanup(func() { s.DB.Pool().Exec(ctx, `DELETE FROM hatamoto_digest_state WHERE name = $1`, DigestName) })
	s.DB.Pool().Exec(ctx, `DELETE FROM hatamoto_digest_state WHERE name = $1`, DigestName)
	day := time.Now().UTC().Truncate(24 * time.Hour)
	first, err := s.ClaimDigest(ctx, day)
	if err != nil || !first {
		t.Fatalf("first claim = %v, %v; want true", first, err)
	}
	if again, err := s.ClaimDigest(ctx, day); err != nil || again {
		t.Fatalf("second claim of the same period = %v, %v; want false", again, err)
	}
	if next, err := s.ClaimDigest(ctx, day.Add(24*time.Hour)); err != nil || !next {
		t.Fatalf("claim of the next period = %v, %v; want true", next, err)
	}
}

func TestDigestSplitsAutomaticCreatesFromUpdates(t *testing.T) {
	s, ctx := newStore(t)
	p := testPlugin(t, s, ctx)
	_, cs := insertRun(t, s, p, "cron", "published", true, time.Now().UTC().Add(-time.Hour))
	items := []struct {
		kind, op, result string
		before           any
		token            any
	}{
		{"entity", "upsert", "applied", nil, nil},              // create
		{"entity", "upsert", "applied", nil, nil},              // create
		{"entity", "upsert", "applied", `{"name":"x"}`, "tok"}, // update
		{"term", "upsert", "applied", nil, nil},                // not an entity create by kind
		{"entity", "upsert", "skipped", `{"name":"y"}`, "tok"}, // not applied
	}
	for i, it := range items {
		if _, err := s.DB.Pool().Exec(ctx, `
			INSERT INTO hatamoto_changeset_items (changeset_id, seq, kind, op, source_key, result, before, before_token)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)`,
			cs, i, it.kind, it.op, "k"+string(rune('a'+i)), it.result, it.before, it.token); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Digest(ctx, time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range got {
		if d.Plugin == p {
			if d.Created != 2 || d.Changes != 2 {
				t.Fatalf("Created=%d Changes=%d, want 2 and 2", d.Created, d.Changes)
			}
			return
		}
	}
	t.Fatal("run missing from the digest")
}
