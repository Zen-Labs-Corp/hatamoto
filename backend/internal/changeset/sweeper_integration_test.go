//go:build integration

package changeset

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func (f *fixture) age(key string, d time.Duration) {
	f.t.Helper()
	old := f.now.Add(-d) // the sweeper judges file age by the engine clock
	if err := os.Chtimes(filepath.Join(f.dir, key), old, old); err != nil {
		f.t.Fatal(err)
	}
}

func TestChangesetSweepExpiresAndRemovesOrphans(t *testing.T) {
	f := newFixture(t)
	t0 := f.now

	// Expired changeset with a staged file.
	runExp := uuid.New()
	exp, err := f.eng.Begin(f.ctx, runExp, "")
	if err != nil {
		t.Fatal(err)
	}
	expKey := f.stageFile(runExp, "x.png")
	f.stage(exp, Item{Kind: KindMedia, Op: OpUpsert, BlobKeys: []string{expKey}, Payload: raw(map[string]any{"filename": "x.png"})})

	// In-flight run: staged, item records its file.
	runLive := uuid.New()
	live, err := f.eng.Begin(f.ctx, runLive, "")
	if err != nil {
		t.Fatal(err)
	}
	liveKey := f.stageFile(runLive, "l.png")
	f.stage(live, Item{Kind: KindMedia, Op: OpUpsert, BlobKeys: []string{liveKey}, Payload: raw(map[string]any{"filename": "l.png"})})
	// A second file of the live run not yet recorded on an item: its run is live, so it stays.
	unrecorded := f.stageFile(runLive, "u.png")

	// Crash leftovers: an old file of an unknown run, and a young one.
	orphan := f.stageFile(uuid.New(), "o.png")
	young := f.stageFile(uuid.New(), "y.png")

	f.now = t0.Add(TTL + time.Minute)
	for _, k := range []string{expKey, liveKey, unrecorded, orphan} {
		f.age(k, 3*time.Hour)
	}
	f.age(young, 10*time.Minute)
	// The live changeset must outlive the sweep: give it a later expiry.
	if _, err := f.db.Pool().Exec(f.ctx, `UPDATE hatamoto_changesets SET expires_at = $2 WHERE id = $1`, live, t0.Add(2*TTL)); err != nil {
		t.Fatal(err)
	}
	// Two replicas sweeping at once.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- f.eng.Sweep(f.ctx)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent sweep: %v", err)
		}
	}

	if n := f.count(`SELECT count(*) FROM hatamoto_changeset_items WHERE changeset_id = $1`, exp); n != 0 {
		t.Errorf("expired changeset kept %d items", n)
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_runs WHERE id = $1`, runExp); n != 0 {
		t.Error("run of the expired changeset was not deleted")
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_runs WHERE id = $1`, runLive); n != 1 {
		t.Error("run of a staged changeset was deleted")
	}
	if f.fileExists(expKey) {
		t.Error("file of an expired changeset survived")
	}
	if f.fileExists(orphan) {
		t.Error("old orphan survived")
	}
	for name, k := range map[string]string{"in-flight": liveKey, "unrecorded in live run": unrecorded, "young orphan": young} {
		if !f.fileExists(k) {
			t.Errorf("%s file was removed", name)
		}
	}
	if got := f.state(live); got != StateStaged {
		t.Errorf("live changeset state = %s", got)
	}
	_ = f.eng.Discard(f.ctx, live)
}
