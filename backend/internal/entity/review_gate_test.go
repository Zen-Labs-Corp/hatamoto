package entity

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authztest"
)

// fakeGate holds every change it is asked about (when hold) and records what it saw.
type fakeGate struct {
	hold    bool
	planErr error
	plans   []ReviewKind
	subs    []ReviewChange
	id      int64
}

func (g *fakeGate) Plan(_ context.Context, _ *Entity, k ReviewKind) (ReviewPlan, error) {
	g.plans = append(g.plans, k)
	if g.planErr != nil {
		return ReviewPlan{}, g.planErr
	}
	return ReviewPlan{Held: g.hold, Stages: []ReviewStage{{Name: "Review", Group: "reviewers"}}}, nil
}

func (g *fakeGate) Submit(_ context.Context, c ReviewChange, _ ReviewPlan) (*SentForReviewError, error) {
	g.subs = append(g.subs, c)
	return &SentForReviewError{ChangesetID: g.id, Since: time.Unix(1, 0)}, nil
}

func gateRig(t *testing.T, hold bool) (*authzRig, *fakeGate, *authztest.Recorder) {
	t.Helper()
	g := newAuthzRig(t)
	gate := &fakeGate{hold: hold, id: 42}
	g.svc.SetReviewGate(gate)
	rec := &authztest.Recorder{}
	authztest.Use(t, rec)
	return g, gate, rec
}

func sent(t *testing.T, err error, id int64) {
	t.Helper()
	var s *SentForReviewError
	if !errors.Is(err, ErrSentForReview) || !errors.As(err, &s) || s.ChangesetID != id {
		t.Fatalf("err = %v, want SentForReviewError %d", err, id)
	}
}

func asked(rec *authztest.Recorder, act authz.Action) bool {
	for _, c := range rec.Calls() {
		if c.Action == act {
			return true
		}
	}
	return false
}

func TestReviewGateHoldsUpdateOfPublished(t *testing.T) {
	g, gate, rec := gateRig(t, true)
	me := uuid.New()
	e := g.entity(t, &me, StatusPublished)
	name := "Changed"
	attrs := jsontext.Value(`{"title":"New","body":"W"}`)
	_, err := g.svc.UpdateEntity(asRole("author", me), e.ID, &me, &UpdateEntityInput{Name: &name, Attributes: &attrs})
	sent(t, err, 42)
	if g.repo.updated != 0 {
		t.Error("repo Update was called")
	}
	if len(gate.subs) != 1 {
		t.Fatalf("submits = %d", len(gate.subs))
	}
	c := gate.subs[0]
	if c.Kind != ReviewEdit || c.Name != "Changed" || c.Slug != e.Slug || c.Status != StatusPublished || string(c.Attributes) != string(attrs) || c.Entity != e {
		t.Errorf("change = %+v", c)
	}
	if !asked(rec, authz.ActionEntityReviewRequest) || asked(rec, authz.ActionEntityUpdate) {
		t.Errorf("asked %v, want review.request instead of update", rec.Calls())
	}
}

func TestReviewGateNotHeldAsksToday(t *testing.T) {
	g, gate, rec := gateRig(t, false)
	me := uuid.New()
	e := g.entity(t, &me, StatusPublished)
	name := "Changed"
	if _, err := g.svc.UpdateEntity(asRole("owner", me), e.ID, &me, &UpdateEntityInput{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if g.repo.updated != 1 || len(gate.subs) != 0 {
		t.Errorf("updated %d, submits %d", g.repo.updated, len(gate.subs))
	}
	if !asked(rec, authz.ActionEntityUpdate) || asked(rec, authz.ActionEntityReviewRequest) {
		t.Errorf("asked %v", rec.Calls())
	}
}

func TestReviewGateNeedsGateAndSubjectAndLiveContent(t *testing.T) {
	g, gate, _ := gateRig(t, true)
	me := uuid.New()
	name := "N"
	in := func() *UpdateEntityInput { return &UpdateEntityInput{Name: &name} }

	// No subject: a system caller writes directly, the gate is not called.
	pub := g.entity(t, &me, StatusPublished)
	if _, err := g.svc.UpdateEntity(context.Background(), pub.ID, &me, in()); err != nil {
		t.Fatal(err)
	}
	if _, err := g.svc.UpdateEntityStatus(context.Background(), pub.ID, &me, StatusPublished); err != nil {
		t.Fatal(err)
	}
	if _, err := g.svc.ScheduleEntity(context.Background(), pub.ID, &me, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// A draft edit, an unpublish and a machine rewrite are not live writes.
	draft := g.entity(t, &me, StatusDraft)
	if _, err := g.svc.UpdateEntity(asRole("author", me), draft.ID, &me, in()); err != nil {
		t.Fatal(err)
	}
	live2 := g.entity(t, &me, StatusPublished)
	if _, err := g.svc.UpdateEntityStatus(asRole("author", me), live2.ID, &me, StatusDraft); err != nil {
		t.Fatal(err)
	}
	keep := in()
	keep.KeepUpdatedAt = true
	live3 := g.entity(t, &me, StatusPublished)
	if _, err := g.svc.UpdateEntity(asRole("author", me), live3.ID, &me, keep); err != nil {
		t.Fatal(err)
	}
	if len(gate.plans) != 0 {
		t.Errorf("the gate was asked %v", gate.plans)
	}

	// No gate: nothing changes either.
	g2 := newAuthzRig(t)
	e := g2.entity(t, &me, StatusPublished)
	if _, err := g2.svc.UpdateEntity(asRole("author", me), e.ID, &me, in()); err != nil {
		t.Fatal(err)
	}
}

func TestReviewGateHoldsStatusScheduleRestore(t *testing.T) {
	g, gate, rec := gateRig(t, true)
	me := uuid.New()
	ctx := asRole("contributor", me)

	// A contributor's publish of their own draft becomes a request.
	d := g.entity(t, &me, StatusDraft)
	_, err := g.svc.UpdateEntityStatus(ctx, d.ID, &me, StatusPublished)
	sent(t, err, 42)
	if d.Status != StatusDraft {
		t.Errorf("status = %s, want draft", d.Status)
	}
	if c := gate.subs[0]; c.Kind != ReviewPublish || c.Status != StatusPublished {
		t.Errorf("publish change = %+v", c)
	}
	if !asked(rec, authz.ActionEntityReviewRequest) || asked(rec, authz.ActionEntityPublish) {
		t.Errorf("asked %v", rec.Calls())
	}

	at := time.Now().Add(time.Hour)
	_, err = g.svc.ScheduleEntity(ctx, d.ID, &me, at)
	sent(t, err, 42)
	if g.repo.scheduled != 0 {
		t.Error("repo Schedule was called")
	}
	if c := gate.subs[1]; c.Kind != ReviewSchedule || c.Status != StatusScheduled || c.PublishedAt == nil || !c.PublishedAt.Equal(at) {
		t.Errorf("schedule change = %+v", c)
	}

	// Restore of a published entity.
	p := g.entity(t, &me, StatusPublished)
	old := jsontext.Value(`{"title":"Old","body":"B"}`)
	g.repo.versions[p.ID] = []*EntityVersion{{ID: uuid.New(), EntityID: p.ID, Version: 1, Attributes: old}}
	_, err = g.svc.RestoreVersion(ctx, p.ID, &me, 1)
	sent(t, err, 42)
	if g.repo.updated != 0 {
		t.Error("repo Update was called by a held restore")
	}
	if c := gate.subs[2]; c.Kind != ReviewRestore || string(c.Attributes) != string(old) || c.Status != StatusPublished {
		t.Errorf("restore change = %+v", c)
	}
	if !asked(rec, authz.ActionEntityReviewRequest) || asked(rec, authz.ActionEntityVersionRestore) {
		t.Errorf("asked %v", rec.Calls())
	}
}

func TestReviewGateRequestStillNeedsTheRight(t *testing.T) {
	g, gate, _ := gateRig(t, true)
	me, other := uuid.New(), uuid.New()
	e := g.entity(t, &other, StatusPublished)
	name := "N"
	// An author has no request right on another author's entry.
	if _, err := g.svc.UpdateEntity(asRole("author", me), e.ID, &me, &UpdateEntityInput{Name: &name}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("err = %v, want ErrNotAllowed", err)
	}
	if len(gate.subs) != 0 {
		t.Error("a refused person's change was staged")
	}
}

func TestReviewGateUnsupportedAndPolicyError(t *testing.T) {
	g, gate, _ := gateRig(t, true)
	me := uuid.New()
	ctx := asRole("author", me)
	e := g.entity(t, &me, StatusPublished)
	parent := uuid.New()
	e.ParentID = &parent
	yes := true
	meta := jsontext.Value(`{"k":1}`)
	for name, in := range map[string]*UpdateEntityInput{
		"autosave": {Autosave: true},
		"metadata": {Metadata: &meta},
		"author":   {AuthorID: &me},
		"byline":   {BylineOrg: &yes},
		"parent":   {ClearParent: true},
	} {
		if _, err := g.svc.UpdateEntity(ctx, e.ID, &me, in); !errors.Is(err, ErrReviewUnsupported) {
			t.Errorf("%s: err = %v, want ErrReviewUnsupported", name, err)
		}
	}
	if len(gate.subs) != 0 || g.repo.updated != 0 {
		t.Error("an unsupported change was staged or written")
	}

	gate.planErr = errors.New("policy down")
	name := "N"
	if _, err := g.svc.UpdateEntity(ctx, e.ID, &me, &UpdateEntityInput{Name: &name}); !errors.Is(err, ErrReviewHold) {
		t.Errorf("update: err = %v, want ErrReviewHold", err)
	}
	if _, err := g.svc.UpdateEntityStatus(ctx, e.ID, &me, StatusPublished); !errors.Is(err, ErrReviewHold) {
		t.Errorf("status: err = %v, want ErrReviewHold", err)
	}
	if g.repo.updated != 0 || len(gate.subs) != 0 {
		t.Error("a policy error let a write through")
	}
}

func TestReviewGateStaleExpectedVersion(t *testing.T) {
	g, gate, _ := gateRig(t, true)
	me := uuid.New()
	e := g.entity(t, &me, StatusPublished)
	name, stale := "N", e.Version+1
	_, err := g.svc.UpdateEntity(asRole("author", me), e.ID, &me, &UpdateEntityInput{Name: &name, ExpectedVersion: &stale})
	if !errors.Is(err, ErrVersionConflict) || len(gate.subs) != 0 {
		t.Fatalf("err = %v, submits %d", err, len(gate.subs))
	}
}

func TestReviewGatePersonPublicFlag(t *testing.T) {
	g, gate, _ := gateRig(t, true)
	sr := g.svc.schemaRepo.(*mockSchemaRepository)
	person := seedSchema(t, sr, func(s *EntitySchema) { s.Name, s.Slug = "Person", personSchemaSlug })
	me := uuid.New()
	p := seedEntity(t, g.repo.mockEntityRepository, person, func(e *Entity) { e.CreatedBy = &me; e.Status = StatusDraft })
	public := jsontext.Value(`{"title":"x","public":true}`)
	hidden := jsontext.Value(`{"title":"x","public":false}`)

	// A draft person whose Public flag publishes it is a publish request.
	_, err := g.svc.UpdateEntity(asRole("author", me), p.ID, &me, &UpdateEntityInput{Attributes: &public})
	sent(t, err, 42)
	if c := gate.subs[0]; c.Kind != ReviewPublish || c.Status != StatusPublished {
		t.Errorf("change = %+v", c)
	}
	if p.Status != StatusDraft || g.repo.updated != 0 {
		t.Error("the person was written")
	}

	// A hidden draft person is not live content.
	if _, err := g.svc.UpdateEntity(asRole("author", me), p.ID, &me, &UpdateEntityInput{Attributes: &hidden}); err != nil {
		t.Fatalf("hidden person: %v", err)
	}

	// A published person made private is an unpublish, written directly.
	p.Status = StatusPublished
	n := len(gate.subs)
	if _, err := g.svc.UpdateEntity(asRole("author", me), p.ID, &me, &UpdateEntityInput{Attributes: &hidden}); err != nil {
		t.Fatalf("private person: %v", err)
	}
	if len(gate.subs) != n {
		t.Error("an unpublish was staged")
	}
}

func TestReviewGateNeverHoldsReuseSchemas(t *testing.T) {
	g, gate, _ := gateRig(t, true)
	sr := g.svc.schemaRepo.(*mockSchemaRepository)
	block := seedSchema(t, sr, func(s *EntitySchema) { s.Name, s.Slug, s.IsBuiltin = "Block", BlockSchemaSlug, true })
	me := uuid.New()
	e := seedEntity(t, g.repo.mockEntityRepository, block, func(e *Entity) { e.CreatedBy = &me; e.Status = StatusDraft })
	name := "N"
	if _, err := g.svc.UpdateEntity(asRole("author", me), e.ID, &me, &UpdateEntityInput{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if len(gate.plans) != 0 {
		t.Errorf("the gate was asked about a block: %v", gate.plans)
	}
}

// Every UpdateEntityInput field is staged, refused or deliberately not carried;
// a new field must be classified here.
func TestReviewGateClassifiesEveryInputField(t *testing.T) {
	const staged, refused, notCarried = "staged", "refused", "not carried"
	want := map[string]string{
		"Name": staged, "Slug": staged, "Locale": staged, "Attributes": staged, "ParentID": staged, "Status": staged,
		"Metadata": refused, "Autosave": refused, "AuthorID": refused, "ClearAuthor": refused,
		"BylinePersonID": refused, "ClearBylinePerson": refused, "BylineOrg": refused, "ClearParent": refused,
		// ChangeSummary: the publish writes its own. ExpectedVersion: checked when the change is held.
		// KeepUpdatedAt: machine writes skip the gate. TakeOwnership: the publish sets the origin.
		"ChangeSummary": notCarried, "ExpectedVersion": notCarried, "KeepUpdatedAt": notCarried, "TakeOwnership": notCarried,
	}
	typ := reflect.TypeOf(UpdateEntityInput{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		class, ok := want[f.Name]
		if !ok {
			t.Errorf("UpdateEntityInput.%s is not classified for review: stage it, refuse it in unsupportedForReview, or list it as not carried", f.Name)
			continue
		}
		if class != refused {
			continue
		}
		in := &UpdateEntityInput{}
		v := reflect.ValueOf(in).Elem().Field(i)
		switch v.Kind() {
		case reflect.Bool:
			v.SetBool(true)
		case reflect.Pointer:
			v.Set(reflect.New(v.Type().Elem()))
			switch p := v.Interface().(type) {
			case *uuid.UUID:
				*p = uuid.New()
			case *bool:
				*p = true
			case *jsontext.Value:
				*p = jsontext.Value(`{"changed":1}`)
			}
		}
		if !unsupportedForReview(storedWithByline(), in) {
			t.Errorf("%s is classified refused but unsupportedForReview ignores a change to it", f.Name)
		}
	}
	if len(want) != typ.NumField() {
		t.Errorf("classification lists %d fields, the input has %d", len(want), typ.NumField())
	}
}

// storedWithByline is a stored row that has every field the input can clear or set.
func storedWithByline() *Entity {
	author, person, parent := uuid.New(), uuid.New(), uuid.New()
	return &Entity{AuthorID: &author, BylinePersonID: &person, ParentID: &parent,
		Metadata: jsontext.Value(`{"k": 1, "z": [true]}`)}
}

// A field sent with the value the row already has is not a change: the classic
// form posts the author on every save and a REST read-modify-write sends the
// whole entity back.
func TestReviewGateUnchangedFieldsAreNotUnsupported(t *testing.T) {
	e := storedWithByline()
	same, org := jsontext.Value(`{"z":[true],"k":1}`), false
	author, person := *e.AuthorID, *e.BylinePersonID
	in := &UpdateEntityInput{Metadata: &same, AuthorID: &author, BylinePersonID: &person, BylineOrg: &org}
	if unsupportedForReview(e, in) {
		t.Error("unchanged byline, author and metadata were refused")
	}
	bare := &Entity{}
	if unsupportedForReview(bare, &UpdateEntityInput{ClearAuthor: true, ClearBylinePerson: true, ClearParent: true}) {
		t.Error("clearing fields that are already empty was refused")
	}
	// "No author" on a row credited to an organisation or to a person without a
	// user account (no AuthorID) is still a byline change.
	nobody := uuid.New()
	for name, row := range map[string]*Entity{"org": {BylineOrg: true}, "person": {BylinePersonID: &nobody}} {
		if !unsupportedForReview(row, &UpdateEntityInput{ClearAuthor: true}) {
			t.Errorf("clearing the %s byline was not refused", name)
		}
	}
	other := jsontext.Value(`{"k":2}`)
	if !unsupportedForReview(e, &UpdateEntityInput{Metadata: &other}) {
		t.Error("changed metadata was not refused")
	}
}

func TestReviewGateStagesUpdateThatResendsStoredByline(t *testing.T) {
	g, gate, _ := gateRig(t, true)
	me := uuid.New()
	e := g.entity(t, &me, StatusPublished)
	person := uuid.New()
	e.BylinePersonID = &person
	name := "Changed"
	_, err := g.svc.UpdateEntity(asRole("author", me), e.ID, &me, &UpdateEntityInput{Name: &name, BylinePersonID: &person})
	sent(t, err, 42)
	if len(gate.subs) != 1 {
		t.Fatalf("submits = %d", len(gate.subs))
	}
	other := uuid.New()
	_, err = g.svc.UpdateEntity(asRole("author", me), e.ID, &me, &UpdateEntityInput{Name: &name, BylinePersonID: &other})
	if !errors.Is(err, ErrReviewUnsupported) {
		t.Errorf("changed byline: err = %v, want ErrReviewUnsupported", err)
	}
}

// A staged publish carries no publication time: only a schedule does.
func TestHeldChangeCarriesPublishedAtOnlyWhenScheduled(t *testing.T) {
	at := time.Now().Add(time.Hour)
	e := &Entity{PublishedAt: &at}
	if c := heldChange(ReviewPublish, e, StatusPublished); c.PublishedAt != nil {
		t.Errorf("publish carries %v", c.PublishedAt)
	}
	if c := heldChange(ReviewEdit, e, StatusScheduled); c.PublishedAt == nil || !c.PublishedAt.Equal(at) {
		t.Errorf("scheduled edit carries %v", c.PublishedAt)
	}
}
