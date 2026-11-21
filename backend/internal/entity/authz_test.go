package entity

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/authzsubject"
	"hatamoto.dev/core/internal/authztest"
)

// scheduleRepo adds Schedule to the mock and counts the writes that reach it.
type scheduleRepo struct {
	*mockEntityRepository
	scheduled, updated int
}

func (r *scheduleRepo) Schedule(_ context.Context, id uuid.UUID, _ *uuid.UUID, _ time.Time) (*Entity, error) {
	r.scheduled++
	e := r.entities[id]
	e.Status = StatusScheduled
	return e, nil
}

func (r *scheduleRepo) Update(ctx context.Context, id uuid.UUID, u *uuid.UUID, in *UpdateEntityInput) (*Entity, error) {
	r.updated++
	return r.mockEntityRepository.Update(ctx, id, u, in)
}

type authzRig struct {
	svc    *Service
	repo   *scheduleRepo
	schema *EntitySchema
}

func newAuthzRig(t *testing.T) *authzRig {
	t.Helper()
	sr := newMockSchemaRepository()
	repo := &scheduleRepo{mockEntityRepository: newMockEntityRepository()}
	schema := seedSchema(t, sr)
	return &authzRig{svc: NewService(sr, repo, testLogger()), repo: repo, schema: schema}
}

func (g *authzRig) entity(t *testing.T, creator *uuid.UUID, status EntityStatus) *Entity {
	t.Helper()
	return seedEntity(t, g.repo.mockEntityRepository, g.schema, func(e *Entity) { e.CreatedBy = creator; e.Status = status })
}

func asRole(role string, user uuid.UUID) context.Context {
	return authzctx.With(context.Background(), authzsubject.ForSession(user.String(), role))
}

func TestAuthzAuthorOwnAndOthers(t *testing.T) {
	g := newAuthzRig(t)
	me, other := uuid.New(), uuid.New()
	ctx := asRole("author", me)
	name := "New name"
	in := &UpdateEntityInput{Name: &name}

	own := g.entity(t, &me, StatusDraft)
	if _, err := g.svc.UpdateEntity(ctx, own.ID, &me, in); err != nil {
		t.Fatalf("own: %v", err)
	}
	for label, e := range map[string]*Entity{"other's": g.entity(t, &other, StatusDraft), "NULL-owner": g.entity(t, nil, StatusDraft)} {
		before := g.repo.updated
		_, err := g.svc.UpdateEntity(ctx, e.ID, &me, in)
		if !errors.Is(err, ErrNotAllowed) {
			t.Fatalf("%s: err = %v, want ErrNotAllowed", label, err)
		}
		if g.repo.updated != before {
			t.Errorf("%s: repo Update was called", label)
		}
	}
}

func TestAuthzContributor(t *testing.T) {
	g := newAuthzRig(t)
	me := uuid.New()
	ctx := asRole("contributor", me)
	name := "N"
	in := &UpdateEntityInput{Name: &name}

	draft := g.entity(t, &me, StatusDraft)
	for _, st := range []EntityStatus{StatusPublished, StatusScheduled} {
		_, err := g.svc.UpdateEntityStatus(ctx, draft.ID, &me, st)
		var na *NotAllowedError
		if !errors.As(err, &na) || !errors.Is(err, ErrNotAllowed) {
			t.Fatalf("status %s: err = %v", st, err)
		}
		if got := na.Error(); !strings.Contains(got, "cannot publish this entry") {
			t.Errorf("message = %q", got)
		}
		if draft.Status != StatusDraft {
			t.Errorf("status changed to %s", draft.Status)
		}
	}
	if _, err := g.svc.ScheduleEntity(ctx, draft.ID, &me, time.Now().Add(time.Hour)); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("schedule: err = %v", err)
	}
	if g.repo.scheduled != 0 {
		t.Error("repo Schedule was called")
	}
	for _, st := range []EntityStatus{StatusScheduled, StatusPublished} {
		e := g.entity(t, &me, st)
		if _, err := g.svc.UpdateEntity(ctx, e.ID, &me, in); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("update own %s: err = %v", st, err)
		}
	}
	if _, err := g.svc.UpdateEntity(ctx, draft.ID, &me, in); err != nil {
		t.Errorf("update own draft: %v", err)
	}
	if err := g.svc.DeleteEntity(ctx, draft.ID); err != nil {
		t.Errorf("delete own draft: %v", err)
	}
}

func TestAuthzReviewer(t *testing.T) {
	g := newAuthzRig(t)
	me, other := uuid.New(), uuid.New()
	ctx := asRole("reviewer", me)

	for label, creator := range map[string]*uuid.UUID{"other's": &other, "NULL-owner": nil} {
		e := g.entity(t, creator, StatusDraft)
		got, err := g.svc.UpdateEntityStatus(ctx, e.ID, &me, StatusPublished)
		if err != nil || got.Status != StatusPublished {
			t.Fatalf("%s publish: %v", label, err)
		}
		if _, err := g.svc.UpdateEntityStatus(ctx, e.ID, &me, StatusDraft); err != nil {
			t.Fatalf("%s unpublish: %v", label, err)
		}
	}
	if _, err := g.svc.CreateEntity(ctx, &me, &CreateEntityInput{SchemaID: g.schema.ID, Name: "x", Slug: "x"}); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("create: err = %v", err)
	}
	if err := g.svc.DeleteEntity(ctx, g.entity(t, &me, StatusDraft).ID); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("delete: err = %v", err)
	}
}

func TestAuthzRestoreAndUnpublishRefused(t *testing.T) {
	g := newAuthzRig(t)
	me, other := uuid.New(), uuid.New()
	ctx := asRole("author", me)
	e := g.entity(t, &other, StatusPublished)
	g.repo.versions[e.ID] = []*EntityVersion{{ID: uuid.New(), EntityID: e.ID, Version: 1, Attributes: e.Attributes}}
	if _, err := g.svc.RestoreVersion(ctx, e.ID, &me, 1); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("restore: err = %v", err)
	}
	// The refusal comes before the store is touched, so an empty store is enough.
	g.svc.SetUnpublishStore(&UnpublishStore{})
	if err := g.svc.SetUnpublish(ctx, e.ID, time.Now().Add(time.Hour), ""); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("set unpublish: err = %v", err)
	}
	if err := g.svc.ClearUnpublish(ctx, e.ID); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("clear unpublish: err = %v", err)
	}
}

func TestAuthzSystemCallerUnchanged(t *testing.T) {
	g := newAuthzRig(t)
	other := uuid.New()
	ctx := context.Background() // no subject: scheduler, workers, CLI without --as
	e := g.entity(t, &other, StatusDraft)
	name := "N"
	if _, err := g.svc.UpdateEntity(ctx, e.ID, nil, &UpdateEntityInput{Name: &name}); err != nil {
		t.Errorf("update: %v", err)
	}
	if _, err := g.svc.UpdateEntityStatus(ctx, e.ID, nil, StatusPublished); err != nil {
		t.Errorf("publish: %v", err)
	}
	if _, err := g.svc.ScheduleEntity(ctx, e.ID, nil, time.Now().Add(time.Hour)); err != nil {
		t.Errorf("schedule: %v", err)
	}
	if err := g.svc.DeleteEntity(ctx, e.ID); err != nil {
		t.Errorf("delete: %v", err)
	}
	// A read failure does not matter without a subject: the old behaviour (no extra read).
	g.repo.getByIDErr = errors.New("boom")
	if err := g.svc.DeleteEntity(ctx, uuid.New()); errors.Is(err, ErrNotAllowed) {
		t.Errorf("system delete refused: %v", err)
	}
}

func TestAuthzRegisteredAuthorizerErrorFailsClosed(t *testing.T) {
	authztest.Use(t, authztest.Func(func(context.Context, authz.Subject, authz.Action, authz.Object) (bool, error) {
		return true, errors.New("policy unavailable")
	}))
	g := newAuthzRig(t)
	me := uuid.New()
	e := g.entity(t, &me, StatusDraft)
	_, err := g.svc.UpdateEntityStatus(asRole("owner", me), e.ID, &me, StatusPublished)
	var na *NotAllowedError
	if !errors.As(err, &na) || na.Err == nil || !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("err = %v, want a NotAllowedError carrying the authorizer error", err)
	}
}

func TestAuthzRecorderSeesEntityObject(t *testing.T) {
	rec := &authztest.Recorder{}
	authztest.Use(t, rec)
	g := newAuthzRig(t)
	me := uuid.New()
	e := g.entity(t, &me, StatusDraft)
	name := "N"
	if _, err := g.svc.UpdateEntity(asRole("editor", me), e.ID, &me, &UpdateEntityInput{Name: &name}); err != nil {
		t.Fatal(err)
	}
	calls := rec.Calls()
	if len(calls) != 1 || calls[0].Action != authz.ActionEntityUpdate {
		t.Fatalf("calls = %+v", calls)
	}
	o := calls[0].Object
	if o.Kind != "entity" || o.ID != e.ID.String() || o.CreatedBy != me.String() || o.Status != "draft" || o.Schema != g.schema.Slug {
		t.Errorf("object = %+v", o)
	}
}

func TestStatusAction(t *testing.T) {
	cases := []struct {
		from, to EntityStatus
		want     authz.Action
	}{
		{StatusDraft, StatusPublished, authz.ActionEntityPublish},
		{StatusDraft, StatusScheduled, authz.ActionEntityPublish},
		{StatusPublished, StatusDraft, authz.ActionEntityUnpublish},
		{StatusScheduled, StatusArchived, authz.ActionEntityUnpublish},
		{StatusDraft, StatusArchived, authz.ActionEntityUpdate},
		{StatusArchived, StatusDraft, authz.ActionEntityUpdate},
	}
	for _, c := range cases {
		if got := statusAction(c.from, c.to); got != c.want {
			t.Errorf("%s -> %s: %s, want %s", c.from, c.to, got, c.want)
		}
	}
}

// A person's status follows attributes.public, so a Contributor must not publish through it.
func TestAuthzPersonPublicNeedsPublish(t *testing.T) {
	g := newAuthzRig(t)
	sr := g.svc.schemaRepo.(*mockSchemaRepository)
	person := seedSchema(t, sr, func(s *EntitySchema) { s.Name, s.Slug = "Person", personSchemaSlug })
	me := uuid.New()
	public := jsontext.Value(`{"title":"x","public":true}`)
	hidden := jsontext.Value(`{"title":"x","public":false}`)
	owner := asRole("owner", me)

	ctx := asRole("contributor", me)
	if _, err := g.svc.CreateEntity(ctx, &me, &CreateEntityInput{SchemaID: person.ID, Name: "p", Slug: "p", Attributes: public}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("create public: err = %v, want ErrNotAllowed", err)
	}
	mine, err := g.svc.CreateEntity(ctx, &me, &CreateEntityInput{SchemaID: person.ID, Name: "q", Slug: "q", Attributes: hidden})
	if err != nil {
		t.Fatalf("create hidden: %v", err)
	}
	if _, err := g.svc.UpdateEntity(ctx, mine.ID, &me, &UpdateEntityInput{Attributes: &public}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("update to public: err = %v, want ErrNotAllowed", err)
	}
	g.repo.versions[mine.ID] = []*EntityVersion{{ID: uuid.New(), EntityID: mine.ID, Version: 1, Attributes: public}}
	if _, err := g.svc.RestoreVersion(ctx, mine.ID, &me, 1); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("restore public: err = %v, want ErrNotAllowed", err)
	}
	if got := g.repo.entities[mine.ID].Status; got != StatusDraft {
		t.Fatalf("status = %s, want draft", got)
	}

	if _, err := g.svc.UpdateEntity(owner, mine.ID, &me, &UpdateEntityInput{Attributes: &public}); err != nil {
		t.Fatalf("owner update to public: %v", err)
	}
	if _, err := g.svc.CreateEntity(owner, &me, &CreateEntityInput{SchemaID: person.ID, Name: "r", Slug: "r", Attributes: public}); err != nil {
		t.Fatalf("owner create public: %v", err)
	}
}
