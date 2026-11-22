package reviewflow

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/authztest"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/reviewstate"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/user"
)

type fakeSwitch struct {
	on  bool
	err error
}

func (f fakeSwitch) ReviewEnabled(context.Context) (bool, error) { return f.on, f.err }

type fakeEngine struct {
	Engine  // the decision methods are not used by the gate
	opts    changeset.EditOptions
	item    changeset.Item
	err     error
	pending changeset.EditRow
}

func (f *fakeEngine) SubmitEdit(_ context.Context, o changeset.EditOptions, it changeset.Item) (int64, error) {
	f.opts, f.item = o, it
	return 42, f.err
}

func (f *fakeEngine) PendingEdit(context.Context, string) (changeset.EditRow, bool, error) {
	return f.pending, f.pending.ID != 0, nil
}

type fakeUsers struct{ u *user.User }

func (f fakeUsers) GetByID(context.Context, uuid.UUID) (*user.User, error) { return f.u, nil }

// session is a context for a signed-in user with the role; the default authorizer answers the bypass.
func session(role string) context.Context {
	return authzctx.With(context.Background(), authz.Subject{UserID: uuid.NewString(), Role: role})
}

func testEntity() *entity.Entity {
	return &entity.Entity{
		ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Name: "Home", Slug: "home", Locale: "en",
		Status: entity.StatusPublished, Version: 3, UpdatedAt: time.Date(2026, 11, 22, 10, 0, 0, 5, time.UTC),
		Schema: &entity.EntitySchema{Slug: "page"},
	}
}

func TestPlan(t *testing.T) {
	authztest.Use(t, &authztest.Recorder{})
	reviewstate.Reset()
	t.Cleanup(reviewstate.Reset)

	cases := []struct {
		name string
		on   bool
		ctx  context.Context
		held bool
	}{
		{"off author", false, session("author"), false},
		{"on owner", true, session("owner"), false},
		{"on reviewer", true, session("reviewer"), false},
		{"on editor", true, session("editor"), true},
		{"on author", true, session("author"), true},
		{"on contributor", true, session("contributor"), true},
		{"on system", true, context.Background(), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &Flow{Switch: fakeSwitch{on: c.on}}
			p, err := f.Plan(c.ctx, testEntity(), entity.ReviewEdit)
			if err != nil {
				t.Fatal(err)
			}
			if p.Held != c.held {
				t.Fatalf("Held = %v, want %v", p.Held, c.held)
			}
			if c.held && (len(p.Stages) != 1 || p.Stages[0] != (entity.ReviewStage{Name: "Review", Group: "reviewers"})) {
				t.Fatalf("stages = %+v, want one Review/reviewers stage", p.Stages)
			}
		})
	}
}

// A site that was never configured has no settings row: review is off, not an error.
func TestPlanUnconfiguredSiteIsOff(t *testing.T) {
	authztest.Use(t, &authztest.Recorder{})
	reviewstate.Reset()
	t.Cleanup(reviewstate.Reset)
	f := &Flow{Switch: fakeSwitch{err: siteconfig.ErrNotConfigured}}
	p, err := f.Plan(session("author"), testEntity(), entity.ReviewEdit)
	if err != nil || p.Held {
		t.Fatalf("Plan = %+v, %v; want not held", p, err)
	}
}

func TestPlanKeyUsesCreatorRole(t *testing.T) {
	authztest.Use(t, &authztest.Recorder{})
	reviewstate.Reset()
	t.Cleanup(reviewstate.Reset)
	var seen reviewstate.Edit
	reviewstate.Register(policyFunc(func(_ context.Context, e reviewstate.Edit) ([]reviewstate.Stage, error) {
		seen = e
		return nil, nil
	}))
	ctx := authzctx.With(context.Background(), authz.Subject{UserID: "u1", KeyID: "k1", Scopes: []string{"*"}, CreatorRole: "author"})
	f := &Flow{Switch: fakeSwitch{on: true}}
	if _, err := f.Plan(ctx, testEntity(), entity.ReviewPublish); err != nil {
		t.Fatal(err)
	}
	if seen.RequesterRole != "author" || seen.Requester != "u1" || seen.Schema != "page" || !seen.ReviewEnabled || seen.Bypass {
		t.Fatalf("policy saw %+v", seen)
	}
}

func TestPlanPolicySeesLocaleAndSection(t *testing.T) {
	authztest.Use(t, &authztest.Recorder{})
	reviewstate.Reset()
	t.Cleanup(reviewstate.Reset)
	var seen reviewstate.Edit
	reviewstate.Register(policyFunc(func(_ context.Context, e reviewstate.Edit) ([]reviewstate.Stage, error) {
		seen = e
		return nil, nil
	}))
	f := &Flow{Switch: fakeSwitch{on: true}, Section: func(context.Context, *entity.Entity) (string, error) { return "/pricing", nil }}
	if _, err := f.Plan(session("author"), testEntity(), entity.ReviewEdit); err != nil {
		t.Fatal(err)
	}
	if seen.Locale != "en" || seen.Section != "/pricing" {
		t.Fatalf("policy saw Locale %q, Section %q", seen.Locale, seen.Section)
	}
}

// A section the core could not work out must not reach the policy as "": the
// edit is held, or a policy would route it to a weaker chain.
func TestPlanSectionFailureHoldsTheEdit(t *testing.T) {
	authztest.Use(t, &authztest.Recorder{})
	reviewstate.Reset()
	t.Cleanup(reviewstate.Reset)
	reviewstate.Register(policyFunc(func(context.Context, reviewstate.Edit) ([]reviewstate.Stage, error) {
		t.Fatal("policy asked although the section is unknown")
		return nil, nil
	}))
	f := &Flow{Switch: fakeSwitch{on: true}, Section: func(context.Context, *entity.Entity) (string, error) {
		return "", errors.New("registry down")
	}}
	if _, err := f.Plan(session("author"), testEntity(), entity.ReviewEdit); !errors.Is(err, reviewstate.ErrPolicy) {
		t.Fatalf("Plan error = %v, want ErrPolicy", err)
	}
}

func TestPlanNoPolicySkipsSection(t *testing.T) {
	authztest.Use(t, &authztest.Recorder{})
	reviewstate.Reset()
	t.Cleanup(reviewstate.Reset)
	f := &Flow{Switch: fakeSwitch{on: true}, Section: func(context.Context, *entity.Entity) (string, error) {
		t.Fatal("Section asked with no policy registered")
		return "", nil
	}}
	if _, err := f.Plan(session("author"), testEntity(), entity.ReviewEdit); err != nil {
		t.Fatal(err)
	}
}

type policyFunc func(context.Context, reviewstate.Edit) ([]reviewstate.Stage, error)

func (p policyFunc) Stages(ctx context.Context, e reviewstate.Edit) ([]reviewstate.Stage, error) {
	return p(ctx, e)
}

func TestPlanRegisteredPolicy(t *testing.T) {
	authztest.Use(t, &authztest.Recorder{})
	reviewstate.Reset()
	t.Cleanup(reviewstate.Reset)
	reviewstate.Register(policyFunc(func(context.Context, reviewstate.Edit) ([]reviewstate.Stage, error) {
		return []reviewstate.Stage{{Name: "Legal", Group: "legal"}}, nil
	}))
	// A policy may stage an owner, and even with the switch off.
	f := &Flow{Switch: fakeSwitch{on: false}}
	p, err := f.Plan(session("owner"), testEntity(), entity.ReviewEdit)
	if err != nil || !p.Held || p.Stages[0].Group != "legal" {
		t.Fatalf("Plan = %+v, %v; want held by the policy's stage", p, err)
	}
}

// Review off and no policy: Plan answers "not held" without asking the
// authorizer, so an authorizer that errors on the new actions cannot refuse
// writes on a site that does not use review.
func TestPlanOffDoesNotAskAuthorizer(t *testing.T) {
	boom := errors.New("unknown action")
	authztest.Use(t, authztest.Func(func(context.Context, authz.Subject, authz.Action, authz.Object) (bool, error) { return false, boom }))
	reviewstate.Reset()
	t.Cleanup(reviewstate.Reset)
	p, err := (&Flow{Switch: fakeSwitch{on: false}}).Plan(session("editor"), testEntity(), entity.ReviewEdit)
	if err != nil || p.Held {
		t.Fatalf("Plan = %+v, %v; want not held, no error", p, err)
	}
}

func TestPlanFailsClosed(t *testing.T) {
	boom := errors.New("boom")
	t.Run("policy", func(t *testing.T) {
		authztest.Use(t, &authztest.Recorder{})
		reviewstate.Reset()
		t.Cleanup(reviewstate.Reset)
		reviewstate.Register(policyFunc(func(context.Context, reviewstate.Edit) ([]reviewstate.Stage, error) { return nil, boom }))
		_, err := (&Flow{Switch: fakeSwitch{on: true}}).Plan(session("owner"), testEntity(), entity.ReviewEdit)
		if !errors.Is(err, reviewstate.ErrPolicy) {
			t.Fatalf("err = %v, want ErrPolicy", err)
		}
	})
	t.Run("switch", func(t *testing.T) {
		authztest.Use(t, &authztest.Recorder{})
		_, err := (&Flow{Switch: fakeSwitch{err: boom}}).Plan(session("author"), testEntity(), entity.ReviewEdit)
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the switch error", err)
		}
	})
	t.Run("authorizer", func(t *testing.T) {
		authztest.Use(t, authztest.Func(func(context.Context, authz.Subject, authz.Action, authz.Object) (bool, error) { return false, boom }))
		_, err := (&Flow{Switch: fakeSwitch{on: true}}).Plan(session("author"), testEntity(), entity.ReviewEdit)
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the authorizer error", err)
		}
	})
}

func TestSubmitPayload(t *testing.T) {
	eng := &fakeEngine{}
	f := &Flow{Engine: eng, Now: func() time.Time { return time.Date(2026, 11, 22, 11, 0, 0, 0, time.UTC) }}
	e := testEntity()
	at := time.Date(2026, 12, 1, 8, 0, 0, 0, time.UTC)
	ctx := authzctx.With(context.Background(), authz.Subject{UserID: "u-author", Role: "author"})
	sent, err := f.Submit(ctx, entity.ReviewChange{
		Kind: entity.ReviewSchedule, Entity: e, Name: "Home 2", Slug: "home", Locale: "en",
		Attributes: jsontext.Value(`{"a":1}`), Status: entity.StatusScheduled, PublishedAt: &at,
	}, entity.ReviewPlan{Held: true, Stages: []entity.ReviewStage{{Name: "Review", Group: "reviewers"}}})
	if err != nil {
		t.Fatal(err)
	}
	if sent.ChangesetID != 42 || !errors.Is(sent, entity.ErrSentForReview) || sent.Since.IsZero() {
		t.Fatalf("sent = %+v", sent)
	}
	if eng.opts.Requester != "u-author" || len(eng.opts.Stages) != 1 || eng.opts.Stages[0].Group != "reviewers" {
		t.Fatalf("options = %+v", eng.opts)
	}
	it := eng.item
	if it.Kind != changeset.KindEntity || it.Op != changeset.OpUpsert || it.TargetID != e.ID.String() {
		t.Fatalf("item = %+v", it)
	}
	if want := "3:2026-11-22T10:00:00.000000005Z"; it.BaseToken != want {
		t.Fatalf("BaseToken = %q, want %q", it.BaseToken, want)
	}
	const want = `{"schema":"page","name":"Home 2","slug":"home","locale":"en","attributes":{"a":1},"parent_id":null,"status":"scheduled","published_at":"2026-12-01T08:00:00Z"}`
	if string(it.Payload) != want {
		t.Fatalf("payload = %s\nwant      %s", it.Payload, want)
	}
}

func TestSubmitPending(t *testing.T) {
	other := uuid.New()
	name := "Robin"
	eng := &fakeEngine{err: changeset.ErrEditPending, pending: changeset.EditRow{ID: 7, Requester: other.String()}}
	f := &Flow{Engine: eng, Users: fakeUsers{u: &user.User{ID: other, Name: &name}}}
	ctx := authzctx.With(context.Background(), authz.Subject{UserID: "u-author", Role: "author"})
	_, err := f.Submit(ctx, entity.ReviewChange{Entity: testEntity(), Status: entity.StatusPublished}, entity.ReviewPlan{Held: true})
	if !errors.Is(err, entity.ErrReviewPending) {
		t.Fatalf("err = %v, want ErrReviewPending", err)
	}
	if got, want := err.Error(), "Robin's change to this entry is waiting for review."; got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
	// An unreadable requester still refuses, without a name.
	f.Users = nil
	_, err = f.Submit(ctx, entity.ReviewChange{Entity: testEntity(), Status: entity.StatusPublished}, entity.ReviewPlan{Held: true})
	if !errors.Is(err, entity.ErrReviewPending) {
		t.Fatalf("err = %v, want ErrReviewPending", err)
	}
}

func TestSubmitNeedsRequester(t *testing.T) {
	_, err := (&Flow{Engine: &fakeEngine{}}).Submit(context.Background(), entity.ReviewChange{Entity: testEntity()}, entity.ReviewPlan{Held: true})
	if err == nil {
		t.Fatal("want an error without a subject")
	}
}
