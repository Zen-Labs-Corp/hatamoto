//go:build integration

package standing

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// insertRun adds a finished run with a changeset in the given state.
func insertRun(t *testing.T, s *Store, plugin, trigger, csState string, auto bool, at time.Time) (uuid.UUID, int64) {
	t.Helper()
	ctx := t.Context()
	id := uuid.New()
	if _, err := s.DB.Pool().Exec(ctx,
		`INSERT INTO hatamoto_runs (id, plugin, state, name, trigger, created_at) VALUES ($1, $2, 'finished', 'prices', $3, $4)`,
		id, plugin, trigger, at); err != nil {
		t.Fatal(err)
	}
	var cs int64
	if err := s.DB.Pool().QueryRow(ctx,
		`INSERT INTO hatamoto_changesets (run_id, plugin, state, expires_at, item_count, auto_published, published_at)
		 VALUES ($1, $2, $3::text, now() + interval '1 day', 4, $4, $5::timestamptz) RETURNING id`,
		id, plugin, csState, auto, at).Scan(&cs); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.DB.Pool().Exec(ctx, `DELETE FROM hatamoto_runs WHERE id = $1`, id)
	})
	return id, cs
}

func TestWaitingListsOnlyStagedCronRuns(t *testing.T) {
	s, ctx := newStore(t)
	p := testPlugin(t, s, ctx)
	now := time.Now().UTC()
	waiting, cs := insertRun(t, s, p, "cron", "staged", false, now)
	insertRun(t, s, p, "cron", "published", true, now)
	insertRun(t, s, p, "owner", "staged", false, now)
	got, err := s.Waiting(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RunID != waiting || got[0].ChangesetID != cs || got[0].Items != 4 || got[0].Run != "prices" {
		t.Fatalf("Waiting = %+v, want only the staged cron run", got)
	}
}

// A run that staged nothing has nothing to review: it must not count as
// waiting, or it would hold back the next tick.
func TestWaitingSkipsAnEmptyChangeset(t *testing.T) {
	s, ctx := newStore(t)
	p := testPlugin(t, s, ctx)
	_, cs := insertRun(t, s, p, "cron", "staged", false, time.Now().UTC())
	if _, err := s.DB.Pool().Exec(ctx, `UPDATE hatamoto_changesets SET item_count = 0 WHERE id = $1`, cs); err != nil {
		t.Fatal(err)
	}
	got, err := s.Waiting(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Waiting = %+v, want none for an empty changeset", got)
	}
}
