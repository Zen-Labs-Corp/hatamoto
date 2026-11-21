//go:build integration

package changeset

import (
	"errors"
	"testing"
	"time"
)

func (f *fixture) beginEdit(stages ...StageSpec) int64 {
	f.t.Helper()
	id, err := f.eng.BeginEdit(f.ctx, EditOptions{Requester: "user-1", Stages: stages})
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func TestEditChangesetNeverExpires(t *testing.T) {
	f := newFixture(t)
	t0 := f.now
	edit := f.beginEdit(StageSpec{Name: "Review", Group: "reviewers"})
	f.stage(edit, f.entityItem("e1-"+f.tag, "edit-"+f.tag, "v1"))
	run := f.begin("")
	f.stage(run, f.entityItem("r1-"+f.tag, "run-"+f.tag, "v1"))

	f.now = t0.Add(TTL + time.Hour)
	if err := f.eng.Sweep(f.ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.state(edit); got != StateStaged {
		t.Errorf("edit state = %s, want staged", got)
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_changesets WHERE id = $1`, run); n != 0 {
		t.Error("the run changeset did not expire in the same sweep")
	}
	f.stage(edit, f.entityItem("e2-"+f.tag, "edit2-"+f.tag, "v1"))

	h, err := f.eng.Header(f.ctx, edit)
	if err != nil {
		t.Fatal(err)
	}
	if h.Kind != ChangesetEdit || h.Requester != "user-1" || !h.ExpiresAt.Equal(NoExpiry) {
		t.Errorf("header = %+v", h)
	}
	_ = f.eng.Discard(f.ctx, edit)
}

func TestRunHeaderKindIsRun(t *testing.T) {
	f := newFixture(t)
	h, err := f.eng.Header(f.ctx, f.begin("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if h.Kind != ChangesetRun || h.Requester != "" {
		t.Errorf("header = %+v", h)
	}
}

func TestBeginEditValidates(t *testing.T) {
	f := newFixture(t)
	for name, o := range map[string]EditOptions{
		"no requester": {Stages: []StageSpec{{Name: "A", Group: "g"}}},
		"no name":      {Requester: "u", Stages: []StageSpec{{Group: "g"}}},
		"no group":     {Requester: "u", Stages: []StageSpec{{Name: "A"}}},
	} {
		if _, err := f.eng.BeginEdit(f.ctx, o); !errors.Is(err, ErrInvalidEdit) {
			t.Errorf("%s: err = %v, want ErrInvalidEdit", name, err)
		}
	}
}

func TestListLeavesEditsOut(t *testing.T) {
	f := newFixture(t)
	edit := f.beginEdit()
	run := f.begin("alpha")
	rows, err := f.eng.List(f.ctx, "", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	var sawRun bool
	for _, r := range rows {
		if r.ID == edit {
			t.Error("List returned an edit changeset")
		}
		sawRun = sawRun || r.ID == run
	}
	if !sawRun {
		t.Error("List dropped a run changeset")
	}
	_ = f.eng.Discard(f.ctx, edit)
	_ = f.eng.Discard(f.ctx, run)
}

func TestEditMayTouchPluginObjects(t *testing.T) {
	f := newFixture(t)
	id := f.alphaObjects()[KindEntity]
	edit := f.beginEdit()
	it := f.entityItem("", "own-"+f.tag, "human")
	it.TargetID = id
	f.stage(edit, it)

	if err := f.eng.Stage(f.ctx, f.begin(""), it); !errors.Is(err, ErrNotOwned) {
		t.Errorf("owner run: err = %v, want not_owned", err)
	}
	_ = f.eng.Discard(f.ctx, edit)
}
