//go:build integration

package changeset

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

func (f *fixture) submit(requester, target string) (int64, error) {
	f.t.Helper()
	it := f.entityItem("", "sub-"+f.tag, "v1")
	it.TargetID = target
	return f.eng.SubmitEdit(f.ctx, EditOptions{Requester: requester, Stages: []StageSpec{{Name: "Review", Group: "reviewers"}}}, it)
}

func (f *fixture) editRows(target string) int {
	return f.count(`SELECT count(*) FROM hatamoto_changesets c JOIN hatamoto_changeset_items i ON i.changeset_id = c.id
		WHERE c.kind = 'edit' AND c.state = 'staged' AND i.target_id = $1`, target)
}

func TestSubmitEdit(t *testing.T) {
	f := newFixture(t)
	target := "target-" + f.tag
	var audited []string
	f.eng.AuditTx = func(_ context.Context, _ pgx.Tx, action, tgt string, d map[string]any) error {
		audited = append(audited, action+" "+tgt)
		return nil
	}
	id, err := f.submit("user-1", target)
	if err != nil {
		t.Fatal(err)
	}
	if f.state(id) != StateStaged || f.editRows(target) != 1 {
		t.Fatalf("state %s rows %d", f.state(id), f.editRows(target))
	}
	if n := f.count(`SELECT item_count FROM hatamoto_changesets WHERE id = $1`, id); n != 1 {
		t.Errorf("item_count = %d", n)
	}
	if len(audited) != 1 || audited[0] != "changeset.edit_submit changeset:"+strconv.FormatInt(id, 10) {
		t.Errorf("audit = %v", audited)
	}

	// The same requester again supersedes.
	id2, err := f.submit("user-1", target)
	if err != nil {
		t.Fatal(err)
	}
	if f.state(id) != StateDiscarded || f.state(id2) != StateStaged || f.editRows(target) != 1 {
		t.Errorf("old %s new %s rows %d", f.state(id), f.state(id2), f.editRows(target))
	}

	// Another requester is refused and nothing changes.
	if _, err := f.submit("user-2", target); !errors.Is(err, ErrEditPending) {
		t.Fatalf("err = %v, want ErrEditPending", err)
	}
	if f.state(id2) != StateStaged || f.editRows(target) != 1 {
		t.Error("the pending edit changed")
	}
	_ = f.eng.Discard(f.ctx, id2)
}

func TestSubmitEditValidates(t *testing.T) {
	f := newFixture(t)
	if _, err := f.eng.SubmitEdit(f.ctx, EditOptions{}, f.entityItem("", "x", "v")); !errors.Is(err, ErrInvalidEdit) {
		t.Errorf("no requester: %v", err)
	}
	if _, err := f.eng.SubmitEdit(f.ctx, EditOptions{Requester: "u"}, f.entityItem("", "x", "v")); !errors.Is(err, ErrInvalidItem) {
		t.Errorf("no target: %v", err)
	}
}

func TestSubmitEditRace(t *testing.T) {
	f := newFixture(t)
	target := "race-" + f.tag
	var wg sync.WaitGroup
	ids := make([]int64, 2)
	errs := make([]error, 2)
	for i, who := range []string{"user-a", "user-b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids[i], errs[i] = f.submit(who, target)
		}()
	}
	wg.Wait()
	ok := 0
	for i := range errs {
		switch {
		case errs[i] == nil:
			ok++
		case !errors.Is(errs[i], ErrEditPending):
			t.Errorf("unexpected error: %v", errs[i])
		}
	}
	if ok != 1 || f.editRows(target) != 1 {
		t.Fatalf("succeeded %d, staged rows %d; want 1 and 1", ok, f.editRows(target))
	}
	for _, id := range ids {
		if id != 0 {
			_ = f.eng.Discard(f.ctx, id)
		}
	}
}

func TestSubmitEditAuditErrorRollsBack(t *testing.T) {
	f := newFixture(t)
	target := "audit-" + f.tag
	first, err := f.submit("user-1", target)
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("sink veto")
	f.eng.AuditTx = func(context.Context, pgx.Tx, string, string, map[string]any) error { return boom }
	before := f.count(`SELECT count(*) FROM hatamoto_changesets`)
	if _, err := f.submit("user-1", target); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the sink error", err)
	}
	if f.state(first) != StateStaged || f.editRows(target) != 1 {
		t.Error("the veto did not roll back the supersede")
	}
	if after := f.count(`SELECT count(*) FROM hatamoto_changesets`); after != before {
		t.Errorf("changesets %d -> %d", before, after)
	}
	_ = f.eng.Discard(f.ctx, first)
}
