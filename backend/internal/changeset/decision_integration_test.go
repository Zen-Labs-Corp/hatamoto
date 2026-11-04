//go:build integration

package changeset

import (
	"errors"
	"testing"
)

func (f *fixture) redirectItem(src, target string) Item {
	return Item{Kind: KindRedirect, Op: OpUpsert, Payload: raw(map[string]any{"source_path": "/" + f.tag + src, "target": target})}
}

func (f *fixture) redirects() int {
	return f.count(`SELECT count(*) FROM hatamoto_redirects WHERE source_path LIKE '/' || $1 || '%'`, f.tag)
}

func (f *fixture) itemResult(cs int64, seq int) string {
	f.t.Helper()
	var r string
	if err := f.db.Pool().QueryRow(f.ctx, `SELECT COALESCE(result, '') FROM hatamoto_changeset_items WHERE changeset_id = $1 AND seq = $2`, cs, seq).Scan(&r); err != nil {
		f.t.Fatal(err)
	}
	return r
}

func TestPublishReviewedUndecided(t *testing.T) {
	f := newFixture(t)
	cs := f.begin("")
	f.stage(cs, f.entityItem("a-"+f.tag, "rev-a-"+f.tag, "ok"))
	f.stage(cs, f.redirectItem("-old", "/elsewhere"))

	if _, err := f.eng.PublishReviewed(f.ctx, cs); !errors.Is(err, ErrUndecided) {
		t.Fatalf("PublishReviewed err = %v, want ErrUndecided", err)
	}
	if n := f.entities(); n != 0 {
		t.Errorf("entities = %d, want 0", n)
	}
	if n := f.redirects(); n != 0 {
		t.Errorf("redirects = %d, want 0", n)
	}
	if s := f.state(cs); s != StateStaged {
		t.Errorf("state = %q, want staged", s)
	}
	// The refused publish rolled its own classification back; Classify saves it.
	if err := f.eng.Classify(f.ctx, cs); err != nil {
		t.Fatal(err)
	}
	var low, high int
	f.db.Pool().QueryRow(f.ctx, `SELECT count(*) FILTER (WHERE risk = 'low'), count(*) FILTER (WHERE risk = 'high') FROM hatamoto_changeset_items WHERE changeset_id = $1`, cs).Scan(&low, &high)
	if low != 1 || high != 1 {
		t.Errorf("classified low=%d high=%d, want 1 and 1", low, high)
	}
}

func TestPublishReviewedApproved(t *testing.T) {
	f := newFixture(t)
	cs := f.begin("")
	f.stage(cs, f.entityItem("a-"+f.tag, "rev-b-"+f.tag, "ok"))
	f.stage(cs, f.redirectItem("-old", "/elsewhere"))

	if err := f.eng.SetDecisions(f.ctx, cs, []int{2}, DecisionApproved); err != nil {
		t.Fatal(err)
	}
	res, err := f.eng.PublishReviewed(f.ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 2 || res.Dropped != 0 {
		t.Errorf("applied %d dropped %d, want 2 and 0", res.Applied, res.Dropped)
	}
	if f.entities() != 1 || f.redirects() != 1 || f.state(cs) != StatePublished {
		t.Errorf("entities %d redirects %d state %q", f.entities(), f.redirects(), f.state(cs))
	}
}

func TestPublishReviewedDropped(t *testing.T) {
	f := newFixture(t)
	cs := f.begin("")
	f.stage(cs, f.entityItem("a-"+f.tag, "rev-keep-"+f.tag, "ok"))
	f.stage(cs, f.entityItem("b-"+f.tag, "rev-drop-"+f.tag, "ok"))
	if err := f.eng.SetDecisions(f.ctx, cs, []int{2}, DecisionDropped); err != nil {
		t.Fatal(err)
	}
	res, err := f.eng.PublishReviewed(f.ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 || res.Dropped != 1 {
		t.Errorf("applied %d dropped %d, want 1 and 1", res.Applied, res.Dropped)
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_entities WHERE schema_id = $1 AND slug = $2`, f.schema.ID, "rev-drop-"+f.tag); n != 0 {
		t.Errorf("dropped entity was created")
	}
	if r := f.itemResult(cs, 2); r != ResultDropped {
		t.Errorf("result = %q, want dropped", r)
	}
	if r := f.itemResult(cs, 1); r != ResultApplied {
		t.Errorf("result = %q, want applied", r)
	}
}

func TestPublishReviewedRedirectGroup(t *testing.T) {
	f := newFixture(t)
	cs := f.begin("")
	f.stage(cs, f.entityItem("a-"+f.tag, "rev-page-"+f.tag, "ok"))
	f.stage(cs, f.redirectItem("-one", "/rev-page-"+f.tag))
	f.stage(cs, f.redirectItem("-two", "/rev-page-"+f.tag))
	f.stage(cs, f.redirectItem("-other", "/elsewhere"))
	if err := f.eng.Classify(f.ctx, cs); err != nil {
		t.Fatal(err)
	}
	var ref int
	f.db.Pool().QueryRow(f.ctx, `SELECT COALESCE(risk_ref, 0) FROM hatamoto_changeset_items WHERE changeset_id = $1 AND seq = 2`, cs).Scan(&ref)
	if ref != 1 {
		t.Fatalf("risk_ref = %d, want 1", ref)
	}
	// A group is decided whole.
	if err := f.eng.SetDecisions(f.ctx, cs, []int{2}, DecisionApproved); !errors.Is(err, ErrNotDecidable) {
		t.Fatalf("SetDecisions on part of a group err = %v, want ErrNotDecidable", err)
	}
	// Dropping the page drops both grouped redirects, which need no decision of their own.
	if err := f.eng.SetDecisions(f.ctx, cs, []int{1}, DecisionDropped); err != nil {
		t.Fatal(err)
	}
	if err := f.eng.SetDecisions(f.ctx, cs, []int{4}, DecisionApproved); err != nil {
		t.Fatal(err)
	}
	res, err := f.eng.PublishReviewed(f.ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 || res.Dropped != 3 {
		t.Errorf("applied %d dropped %d, want 1 and 3", res.Applied, res.Dropped)
	}
	for _, seq := range []int{2, 3} {
		if r := f.itemResult(cs, seq); r != ResultDropped {
			t.Errorf("seq %d result = %q, want dropped", seq, r)
		}
	}
	if n := f.redirects(); n != 1 {
		t.Errorf("redirects = %d, want 1", n)
	}
}

func TestSetDecisionsNotStaged(t *testing.T) {
	f := newFixture(t)
	cs := f.begin("")
	f.stage(cs, f.entityItem("a-"+f.tag, "rev-c-"+f.tag, "ok"))
	f.publish(cs)
	if err := f.eng.SetDecisions(f.ctx, cs, []int{1}, DecisionApproved); !errors.Is(err, ErrNotStaged) {
		t.Errorf("SetDecisions err = %v, want ErrNotStaged", err)
	}
}

func TestPublishStillPublishesAll(t *testing.T) {
	f := newFixture(t)
	cs := f.begin("")
	f.stage(cs, f.entityItem("a-"+f.tag, "rev-d-"+f.tag, "ok"))
	f.stage(cs, f.redirectItem("-old", "/elsewhere"))
	f.stage(cs, f.entityItem("b-"+f.tag, "rev-e-"+f.tag, "ok"))
	// A drop recorded for review does not touch the old entry point.
	if err := f.eng.SetDecisions(f.ctx, cs, []int{3}, DecisionDropped); err != nil {
		t.Fatal(err)
	}
	res := f.publish(cs)
	if res.Applied != 3 || f.entities() != 2 || f.redirects() != 1 {
		t.Errorf("applied %d entities %d redirects %d, want 3, 2, 1", res.Applied, f.entities(), f.redirects())
	}
}

func TestRevertAfterPublishReviewed(t *testing.T) {
	f := newFixture(t)
	cs := f.begin("")
	f.stage(cs, f.entityItem("a-"+f.tag, "rev-f-"+f.tag, "ok"))
	f.stage(cs, f.entityItem("b-"+f.tag, "rev-g-"+f.tag, "ok"))
	if err := f.eng.SetDecisions(f.ctx, cs, []int{2}, DecisionDropped); err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.PublishReviewed(f.ctx, cs); err != nil {
		t.Fatal(err)
	}
	res, err := f.eng.Revert(f.ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 || res.Skipped != 0 {
		t.Errorf("revert applied %d skipped %d, want 1 and 0", res.Applied, res.Skipped)
	}
	if r := f.itemResult(cs, 2); r != ResultDropped {
		t.Errorf("dropped item result = %q after revert, want dropped", r)
	}
	if r := f.itemResult(cs, 1); r != ResultReverted {
		t.Errorf("applied item result = %q after revert, want reverted", r)
	}
	if f.entities() != 0 || f.state(cs) != StateReverted {
		t.Errorf("entities %d state %q", f.entities(), f.state(cs))
	}
}
