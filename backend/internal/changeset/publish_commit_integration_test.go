//go:build integration

package changeset

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// recordUndo applies through the real writer and reports whether the publish
// ran the item's undo.
type recordUndo struct {
	KindWriter
	undone *bool
}

func (w recordUndo) Apply(ctx context.Context, tx Tx, origin string, it Item, target string) (Outcome, error) {
	out, err := w.KindWriter.Apply(ctx, tx, origin, it, target)
	out.Undo = func(context.Context) { *w.undone = true }
	return out, err
}

// The client cannot cancel the COMMIT of a publish: a cancelled request must
// not cut the commit off (it may have committed anyway) and run the undo, which
// would delete files of rows that exist. A deferred trigger slows the commit
// down; the test cancels the request while the server is inside COMMIT.
func TestPublishCommitIsNotCancelledByTheClient(t *testing.T) {
	f := newFixture(t)
	var undone bool
	f.eng.Writers[KindEntity] = recordUndo{f.eng.Writers[KindEntity], &undone}

	cs := f.begin("acme")
	f.stage(cs, f.entityItem("k"+f.tag, "commit-"+f.tag, "x"))
	for _, q := range []string{
		`CREATE OR REPLACE FUNCTION hm_test_slow_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(1.5); RETURN NULL; END $$`,
		fmt.Sprintf(`CREATE CONSTRAINT TRIGGER hm_test_slow_commit AFTER UPDATE ON hatamoto_changesets
			DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.id = %d AND NEW.state = 'published')
			EXECUTE FUNCTION hm_test_slow_commit()`, cs),
	} {
		if _, err := f.db.Pool().Exec(f.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		f.db.Pool().Exec(context.Background(), `DROP TRIGGER IF EXISTS hm_test_slow_commit ON hatamoto_changesets`)
		f.db.Pool().Exec(context.Background(), `DROP FUNCTION IF EXISTS hm_test_slow_commit()`)
	})

	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	go func() {
		for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
			var n int
			f.db.Pool().QueryRow(context.Background(),
				`SELECT count(*) FROM pg_stat_activity WHERE state = 'active' AND lower(query) = 'commit'`).Scan(&n)
			if n > 0 {
				cancel()
				return
			}
		}
	}()
	if _, err := f.eng.Publish(ctx, cs); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if undone {
		t.Error("the undo ran for a publish that committed")
	}
	if got := f.state(cs); got != StatePublished {
		t.Errorf("state = %s, want published", got)
	}
}
