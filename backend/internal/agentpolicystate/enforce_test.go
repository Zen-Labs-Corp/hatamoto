package agentpolicystate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/auditctx"
	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/authzctx"
)

var testKey = uuid.NewString()

func keyCtx() context.Context {
	return auditctx.With(context.Background(), auditctx.Actor{
		ID: "creator", Name: "api-key:bot", Kind: audit.KindAPIKey, KeyID: testKey, Via: "mcp",
	})
}

func captureRuntime(t *testing.T) *[]auditstate.Event {
	t.Helper()
	var evs []auditstate.Event
	SetRuntime(Runtime{
		Record:  func(_ context.Context, ev auditstate.Event) { evs = append(evs, ev) },
		Changes: func(context.Context, string, time.Time, int) ([]Change, error) { return nil, nil },
	})
	t.Cleanup(Reset)
	return &evs
}

func TestEnforceInactiveOrNotKey(t *testing.T) {
	Reset()
	evs := captureRuntime(t)
	if d, err := Enforce(keyCtx(), ActionPublish, Entity{ID: "e"}, true); err != nil || d.Outcome != OutcomeAllow {
		t.Fatalf("inactive: %+v %v", d, err)
	}
	p := &fakePolicy{d: Decision{Outcome: OutcomeDeny}}
	use(t, p)
	captureRuntime(t)
	for _, ctx := range []context.Context{
		context.Background(),
		auditctx.With(context.Background(), auditctx.Actor{Name: "x@y", Kind: audit.KindUser}),
	} {
		if d, err := Enforce(ctx, ActionPublish, Entity{}, true); err != nil || d.Outcome != OutcomeAllow {
			t.Fatalf("non key: %+v %v", d, err)
		}
	}
	if p.calls != 0 || len(*evs) != 0 {
		t.Fatalf("policy called %d times, %d events", p.calls, len(*evs))
	}
}

func TestEnforceDeny(t *testing.T) {
	use(t, &fakePolicy{d: Decision{Outcome: OutcomeDeny, Reason: "nope"}})
	evs := captureRuntime(t)
	_, err := Enforce(keyCtx(), ActionPublish, Entity{ID: "e1"}, true)
	var de *DeniedError
	if !errors.As(err, &de) || de.Reason != "nope" {
		t.Fatalf("err %v", err)
	}
	if len(*evs) != 1 {
		t.Fatalf("%d events", len(*evs))
	}
	ev := (*evs)[0]
	if ev.Action != DenyAction || ev.Result != audit.ResultDenied || ev.KeyID != testKey || ev.Target != "e1" ||
		ev.Detail["outcome"] != "deny" || ev.Detail["reason"] != "nope" || ev.Detail["via"] != "mcp" || ev.Detail["action"] != "publish" {
		t.Fatalf("event %+v", ev)
	}
}

func TestEnforcePolicyError(t *testing.T) {
	use(t, &fakePolicy{err: errors.New("x")})
	evs := captureRuntime(t)
	if _, err := Enforce(keyCtx(), ActionEdit, Entity{ID: "e"}, true); !errors.Is(err, ErrPolicy) {
		t.Fatalf("err %v", err)
	}
	if len(*evs) != 1 || (*evs)[0].Detail["outcome"] != "error" {
		t.Fatalf("events %+v", *evs)
	}
}

func TestEnforceRequireReview(t *testing.T) {
	use(t, &fakePolicy{d: Decision{Outcome: OutcomeRequireReview, Reason: "look"}})
	evs := captureRuntime(t)
	d, err := Enforce(keyCtx(), ActionPublish, Entity{ID: "e"}, true)
	if err != nil || d.Outcome != OutcomeRequireReview || len(*evs) != 0 {
		t.Fatalf("canStage: %+v %v %d", d, err, len(*evs))
	}
	if _, err := Enforce(keyCtx(), ActionEdit, Entity{ID: "e"}, false); !errors.Is(err, ErrNeedsReview) {
		t.Fatalf("cannot stage: %v", err)
	}
	if len(*evs) != 1 || (*evs)[0].Detail["outcome"] != "needs_review" {
		t.Fatalf("events %+v", *evs)
	}
}

func TestEnforceRecordPanicKeepsRefusal(t *testing.T) {
	use(t, &fakePolicy{d: Decision{Outcome: OutcomeDeny, Reason: "r"}})
	SetRuntime(Runtime{
		Record:  func(context.Context, auditstate.Event) { panic("audit down") },
		Changes: func(context.Context, string, time.Time, int) ([]Change, error) { return nil, nil },
	})
	if _, err := Enforce(keyCtx(), ActionPublish, Entity{}, true); !errors.Is(err, ErrDenied) {
		t.Fatalf("err %v", err)
	}
}

func TestChanges(t *testing.T) {
	Reset()
	if _, err := Changes(context.Background(), "nope", time.Time{}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("invalid: %v", err)
	}
	if _, err := Changes(context.Background(), testKey, time.Time{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no runtime: %v", err)
	}
	gotLimit := 0
	SetRuntime(Runtime{
		Record: func(context.Context, auditstate.Event) {},
		Changes: func(_ context.Context, k string, _ time.Time, limit int) ([]Change, error) {
			gotLimit = limit
			return []Change{{ID: 1, Action: "x"}}, nil
		},
	})
	t.Cleanup(Reset)
	got, err := Changes(context.Background(), testKey, time.Time{})
	if err != nil || len(got) != 1 || gotLimit != MaxChanges {
		t.Fatalf("got %+v %v limit %d", got, err, gotLimit)
	}
	Reset()
	if _, err := Changes(context.Background(), testKey, time.Time{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Reset keeps runtime: %v", err)
	}
}

func TestEnforceKeySubjectWithoutAuditActorFailsClosed(t *testing.T) {
	p := &fakePolicy{d: Decision{Outcome: OutcomeAllow}}
	use(t, p)
	captureRuntime(t)
	ctx := authzctx.With(context.Background(), authz.Subject{UserID: "u", KeyID: testKey})
	if !AsksFor(ctx) {
		t.Fatal("a key subject must be asked for")
	}
	if _, err := Enforce(ctx, ActionEdit, Entity{}, false); !errors.Is(err, ErrPolicy) || p.calls != 0 {
		t.Fatalf("err %v, calls %d", err, p.calls)
	}
	if AsksFor(authzctx.With(context.Background(), authz.Subject{UserID: "u"})) {
		t.Fatal("a person is not asked for")
	}
}
