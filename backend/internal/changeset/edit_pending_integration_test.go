//go:build integration

package changeset

import "testing"

func TestPendingStages(t *testing.T) {
	f := newFixture(t)
	two := []StageSpec{{Name: "One", Group: "g1"}, {Name: "Two", Group: "g2"}}
	mk := func(target string) int64 {
		it := f.entityItem("", "ps-"+target, "v1")
		it.TargetID = target
		id, err := f.eng.SubmitEdit(f.ctx, EditOptions{Requester: "u-ps", Stages: two}, it)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.eng.Discard(f.ctx, id) })
		return id
	}
	ids := map[int64]bool{}
	mine := func() []PendingStageRow {
		t.Helper()
		all, err := f.eng.PendingStages(f.ctx, 10000)
		if err != nil {
			t.Fatal(err)
		}
		var out []PendingStageRow
		for _, r := range all {
			if ids[r.ID] {
				out = append(out, r)
			}
		}
		return out
	}
	tA := "pa-" + f.tag
	a := mk(tA)
	b, c, d, e := mk("pb-"+f.tag), mk("pc-"+f.tag), mk("pd-"+f.tag), mk("pe-"+f.tag)
	for _, id := range []int64{a, b, c, d, e} {
		ids[id] = true
	}

	rows := mine()
	if len(rows) != 5 || rows[0].ID != a {
		t.Fatalf("pending = %+v", rows)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].ID <= rows[i-1].ID {
			t.Errorf("not oldest first: %d after %d", rows[i].ID, rows[i-1].ID)
		}
	}
	r := rows[0]
	if r.Requester != "u-ps" || r.TargetID != tA || r.Name != "N ps-"+tA || r.Schema != f.schema.Slug ||
		len(r.Stages) != 2 || r.Approvals != 0 || r.LastApproval != nil || r.CreatedAt.IsZero() {
		t.Errorf("row = %+v", r)
	}

	if err := f.decide(a, 0, "rev", StageApproved); err != nil {
		t.Fatal(err)
	}
	if rows = mine(); len(rows) != 5 || rows[0].ID != a || rows[0].Approvals != 1 || rows[0].LastApproval == nil {
		t.Errorf("after one approval: %+v", rows)
	}

	if err := f.decide(b, 0, "rev", StageRejected); err != nil { // sent back
		t.Fatal(err)
	}
	for _, s := range []int{0, 1} { // every stage approved
		if err := f.decide(c, s, "rev", StageApproved); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.eng.Discard(f.ctx, d); err != nil { // withdrawn
		t.Fatal(err)
	}
	rows = mine()
	if len(rows) != 2 || rows[0].ID != a || rows[1].ID != e {
		t.Errorf("after reject/approve-all/discard: %+v", rows)
	}

	one, err := f.eng.PendingStages(f.ctx, 1)
	if err != nil || len(one) > 1 {
		t.Errorf("limit 1 = %d rows, %v", len(one), err)
	}
}
