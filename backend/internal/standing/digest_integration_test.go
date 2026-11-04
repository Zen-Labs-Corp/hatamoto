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
