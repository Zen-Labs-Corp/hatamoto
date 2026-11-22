//go:build integration

package changeset

import "testing"

func TestEditReads(t *testing.T) {
	f := newFixture(t)
	base, err := f.eng.CountPendingEdits(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(requester, target string) int64 {
		it := f.entityItem("", "er-"+target, "v1")
		it.TargetID = target
		id, err := f.eng.SubmitEdit(f.ctx, EditOptions{Requester: requester, Stages: []StageSpec{{Name: "Review", Group: "reviewers"}}}, it)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	tA, tB, tC := "ta-"+f.tag, "tb-"+f.tag, "tc-"+f.tag
	a, b, c := mk("u-a", tA), mk("u-b", tB), mk("u-a", tC)
	if err := f.decide(b, 0, "rev", StageRejected); err != nil { // sent back
		t.Fatal(err)
	}
	if err := f.eng.Discard(f.ctx, c); err != nil { // withdrawn
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.eng.Discard(f.ctx, a); _ = f.eng.Discard(f.ctx, b) })

	pending, err := f.eng.ListEdits(f.ctx, EditFilter{Pending: true, Requester: "u-a"}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != a || pending[0].TargetID != tA || pending[0].Name != "N er-"+tA ||
		pending[0].Requester != "u-a" || len(pending[0].Stages) != 1 || pending[0].Approvals != 0 || pending[0].Rejected {
		t.Fatalf("pending of u-a = %+v", pending)
	}
	if rows, _ := f.eng.ListEdits(f.ctx, EditFilter{Pending: true, TargetID: tB}, 50, 0); len(rows) != 0 {
		t.Errorf("a rejected edit is in the queue: %+v", rows)
	}
	all, err := f.eng.ListEdits(f.ctx, EditFilter{TargetID: tB}, 50, 0)
	if err != nil || len(all) != 1 || !all[0].Rejected || all[0].State != StateStaged {
		t.Errorf("unfiltered rows of the rejected target = %+v, %v", all, err)
	}
	if rows, _ := f.eng.ListEdits(f.ctx, EditFilter{Pending: true, TargetID: tC}, 50, 0); len(rows) != 0 {
		t.Errorf("a discarded edit is in the queue: %+v", rows)
	}

	n, err := f.eng.CountPendingEdits(f.ctx)
	if err != nil || n != base+1 {
		t.Errorf("CountPendingEdits = %d, %v; want %d", n, err, base+1)
	}

	row, ok, err := f.eng.PendingEdit(f.ctx, tB)
	if err != nil || !ok || row.ID != b || !row.Rejected {
		t.Errorf("PendingEdit of a rejected edit = %+v, %v, %v", row, ok, err)
	}
	if _, ok, _ := f.eng.PendingEdit(f.ctx, tC); ok {
		t.Error("PendingEdit found a discarded edit")
	}
	if _, ok, _ := f.eng.PendingEdit(f.ctx, ""); ok {
		t.Error("PendingEdit found an edit with no target")
	}
}
