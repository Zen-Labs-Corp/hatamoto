//go:build integration

package changeset

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/reviewstate"
	"hatamoto.dev/core/review"
)

type fakePolicy struct {
	stages []review.Stage
	err    error
}

func (p fakePolicy) Stages(context.Context, review.Edit) ([]review.Stage, error) {
	return p.stages, p.err
}

func registerPolicy(t *testing.T, p fakePolicy) {
	t.Helper()
	reviewstate.Reset()
	t.Cleanup(reviewstate.Reset)
	review.Register(p)
}

// beginPolicyEdit resolves the stages through the review package, as the
// routing caller will, and opens an edit changeset with one staged item.
func (f *fixture) beginPolicyEdit(key string) int64 {
	f.t.Helper()
	stages, err := review.StagesFor(f.ctx, review.Edit{Requester: "u1", ReviewEnabled: true})
	if err != nil {
		f.t.Fatal(err)
	}
	specs := make([]StageSpec, len(stages))
	for i, s := range stages {
		specs[i] = StageSpec{Name: s.Name, Group: s.Group}
	}
	id := f.beginEdit(specs...)
	f.stage(id, f.entityItem(key+"-"+f.tag, key+"-"+f.tag, "v1"))
	return id
}

func (f *fixture) decide(cs int64, stage int, reviewer, decision string) error {
	return f.eng.DecideStage(f.ctx, cs, StageDecision{Stage: stage, Reviewer: reviewer, Decision: decision})
}

var threeStages = []review.Stage{{Name: "A", Group: "ga"}, {Name: "B", Group: "gb"}, {Name: "C", Group: "gc"}}

func TestEditStagesDecidedInOrderThenPublished(t *testing.T) {
	f := newFixture(t)
	registerPolicy(t, fakePolicy{stages: threeStages})
	cs := f.beginPolicyEdit("ordered")

	if _, err := f.eng.Publish(f.ctx, cs); !errors.Is(err, ErrStagesPending) {
		t.Fatalf("publish with no decisions: err = %v, want ErrStagesPending", err)
	}
	if err := f.decide(cs, 1, "r1", StageApproved); !errors.Is(err, ErrStageOrder) {
		t.Fatalf("stage 1 first: err = %v, want ErrStageOrder", err)
	}
	for i, who := range []string{"r0", "r1", "r2"} {
		if err := f.decide(cs, i, who, StageApproved); err != nil {
			t.Fatalf("stage %d: %v", i, err)
		}
		if i < 2 {
			if _, err := f.eng.Publish(f.ctx, cs); !errors.Is(err, ErrStagesPending) {
				t.Fatalf("publish after stage %d: err = %v, want ErrStagesPending", i, err)
			}
		}
	}
	if err := f.decide(cs, 3, "r3", StageApproved); !errors.Is(err, ErrStageOrder) {
		t.Errorf("stage past the last: err = %v, want ErrStageOrder", err)
	}
	got, err := f.eng.StageDecisions(f.ctx, cs)
	if err != nil || len(got) != 3 {
		t.Fatalf("decisions = %+v, %v", got, err)
	}
	for i, d := range got {
		if d.Stage != i || d.Reviewer != []string{"r0", "r1", "r2"}[i] || d.Decision != StageApproved || d.At.IsZero() {
			t.Errorf("decision %d = %+v", i, d)
		}
	}
	if f.state(cs) != StateStaged {
		t.Fatal("a refused publish must leave the edit staged")
	}
	f.publish(cs)
	if f.state(cs) != StatePublished {
		t.Errorf("state = %s, want published", f.state(cs))
	}
}

func TestEditRejectionStopsEverything(t *testing.T) {
	f := newFixture(t)
	registerPolicy(t, fakePolicy{stages: threeStages})
	cs := f.beginPolicyEdit("rejected")

	if err := f.decide(cs, 0, "r0", StageRejected); err != nil {
		t.Fatal(err)
	}
	if err := f.decide(cs, 1, "r1", StageApproved); !errors.Is(err, ErrStageRejected) {
		t.Errorf("decide after reject: err = %v, want ErrStageRejected", err)
	}
	if _, err := f.eng.Publish(f.ctx, cs); !errors.Is(err, ErrStageRejected) {
		t.Errorf("publish after reject: err = %v, want ErrStageRejected", err)
	}
	if f.state(cs) != StateStaged {
		t.Errorf("state = %s, want staged", f.state(cs))
	}
	_ = f.eng.Discard(f.ctx, cs)
}

func TestEditWithoutStagesPublishesAtOnce(t *testing.T) {
	f := newFixture(t)
	cs := f.beginEdit()
	f.stage(cs, f.entityItem("zero-"+f.tag, "zero-"+f.tag, "v1"))
	f.publish(cs)
	if f.state(cs) != StatePublished {
		t.Errorf("state = %s, want published", f.state(cs))
	}
}

func TestEditPublishesOverAPluginObject(t *testing.T) {
	f := newFixture(t)
	id := f.alphaObjects()[KindEntity]
	cs := f.beginEdit()
	it := f.entityItem("", "own-"+f.tag, "human")
	it.TargetID = id
	f.stage(cs, it)
	res := f.publish(cs)
	if res.Applied != 1 || res.Skipped != 0 {
		t.Errorf("result = %+v, want the edit applied", res)
	}
}

func TestPolicyErrorStagesNothing(t *testing.T) {
	f := newFixture(t)
	registerPolicy(t, fakePolicy{err: errors.New("unavailable")})
	before := f.count(`SELECT count(*) FROM hatamoto_changesets`)

	stages, err := review.StagesFor(f.ctx, review.Edit{Requester: "u1", ReviewEnabled: true})
	if !errors.Is(err, review.ErrPolicy) || stages != nil {
		t.Fatalf("got %v, %v; want nil, ErrPolicy", stages, err)
	}
	// The caller stops here: BeginEdit is never called, so nothing is staged.
	if after := f.count(`SELECT count(*) FROM hatamoto_changesets`); after != before {
		t.Errorf("changesets %d -> %d", before, after)
	}
}

func TestDecideStageNeedsAnEdit(t *testing.T) {
	f := newFixture(t)
	run := f.begin("alpha")
	if err := f.decide(run, 0, "r", StageApproved); !errors.Is(err, ErrNotEdit) {
		t.Errorf("err = %v, want ErrNotEdit", err)
	}
	edit := f.beginEdit(StageSpec{Name: "A", Group: "g"})
	if err := f.decide(edit, 0, "", StageApproved); !errors.Is(err, ErrInvalidEdit) {
		t.Errorf("empty reviewer: err = %v, want ErrInvalidEdit", err)
	}
	if err := f.decide(edit, 0, "r", "maybe"); !errors.Is(err, ErrInvalidEdit) {
		t.Errorf("bad decision: err = %v, want ErrInvalidEdit", err)
	}
	_ = f.eng.Discard(f.ctx, run)
	_ = f.eng.Discard(f.ctx, edit)
}

func TestEditTakesNoItemsOnceReviewStarted(t *testing.T) {
	for _, approved := range []int{1, 3} {
		f := newFixture(t)
		registerPolicy(t, fakePolicy{stages: threeStages})
		cs := f.beginPolicyEdit("late")
		for i := 0; i < approved; i++ {
			if err := f.decide(cs, i, "r", StageApproved); err != nil {
				t.Fatal(err)
			}
		}
		late := f.entityItem("late-b-"+f.tag, "late-b-"+f.tag, "v2")
		if err := f.eng.Stage(f.ctx, cs, late); !errors.Is(err, ErrReviewStarted) {
			t.Fatalf("%d approved: stage err = %v, want ErrReviewStarted", approved, err)
		}
		if got := f.count(`SELECT count(*) FROM hatamoto_changeset_items WHERE changeset_id = $1`, cs); got != 1 {
			t.Fatalf("%d approved: %d items, want 1", approved, got)
		}
		if approved == 3 {
			if _, err := f.eng.Publish(f.ctx, cs); err != nil {
				t.Fatal(err)
			}
			if got := f.entities(); got != 1 {
				t.Fatalf("%d entities after publish, want only the first item's", got)
			}
		}
	}
}

func TestDecideStageRefusesTheRequester(t *testing.T) {
	f := newFixture(t)
	for _, decision := range []string{StageApproved, StageRejected} {
		cs := f.beginEdit(StageSpec{Name: "A", Group: "g"}) // requester "user-1"
		f.stage(cs, f.entityItem("self-"+decision+"-"+f.tag, "self-"+decision+"-"+f.tag, "v1"))
		if err := f.decide(cs, 0, "user-1", decision); !errors.Is(err, ErrSelfDecision) {
			t.Errorf("%s: err = %v, want ErrSelfDecision", decision, err)
		}
		if ds, _ := f.eng.StageDecisions(f.ctx, cs); len(ds) != 0 {
			t.Errorf("%s: a decision was recorded", decision)
		}
		if err := f.decide(cs, 0, "someone-else", decision); err != nil {
			t.Errorf("%s by another: %v", decision, err)
		}
	}
}

func TestDecideStageAuditInTransaction(t *testing.T) {
	f := newFixture(t)
	var got []string
	f.eng.AuditTx = func(_ context.Context, _ pgx.Tx, action, target string, d map[string]any) error {
		got = append(got, action+" "+fmt.Sprint(d["decision"], " ", d["has_comment"]))
		return nil
	}
	cs := f.beginEdit(StageSpec{Name: "A", Group: "g"})
	f.stage(cs, f.entityItem("aud-"+f.tag, "aud-"+f.tag, "v1"))
	if err := f.eng.DecideStage(f.ctx, cs, StageDecision{Reviewer: "r", Decision: StageApproved, Comment: "ok"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "changeset.stage_decision approved true" {
		t.Errorf("audit = %v", got)
	}

	// A refused audit rolls the decision back.
	cs2 := f.beginEdit(StageSpec{Name: "A", Group: "g"})
	f.stage(cs2, f.entityItem("aud2-"+f.tag, "aud2-"+f.tag, "v1"))
	f.eng.AuditTx = func(context.Context, pgx.Tx, string, string, map[string]any) error { return errors.New("veto") }
	if err := f.decide(cs2, 0, "r", StageApproved); err == nil {
		t.Fatal("decision went through a refused audit")
	}
	if ds, _ := f.eng.StageDecisions(f.ctx, cs2); len(ds) != 0 {
		t.Error("the decision survived the veto")
	}
}
