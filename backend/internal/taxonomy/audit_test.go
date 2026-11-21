package taxonomy

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type auditCall struct {
	action, target string
	detail         map[string]any
}

type auditRepo struct{ mergingRepo }

func (auditRepo) SetPosition(_ context.Context, _ uuid.UUID, pos int) (int, int, error) {
	return pos, pos, nil
}

func recordedService() (*Service, *[]auditCall) {
	svc := NewService(auditRepo{mergingRepo{newFakeRepo()}}, nil)
	var calls []auditCall
	svc.SetAudit(func(_ context.Context, action, target string, detail map[string]any) {
		calls = append(calls, auditCall{action, target, detail})
	})
	return svc, &calls
}

func TestServiceAuditsTermWrites(t *testing.T) {
	ctx := context.Background()
	svc, calls := recordedService()
	a, err := svc.CreateTerm(ctx, CreateTermInput{Kind: "tag", Name: "Alpha"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := svc.CreateTerm(ctx, CreateTermInput{Kind: "tag", Name: "Beta"})
	want := func(i int, action, target string, keys ...string) {
		t.Helper()
		if i >= len(*calls) {
			t.Fatalf("call %d missing; have %d", i, len(*calls))
		}
		c := (*calls)[i]
		if c.action != action || c.target != target || len(c.detail) != len(keys) {
			t.Fatalf("call %d = %+v, want %s %s %v", i, c, action, target, keys)
		}
		for _, k := range keys {
			if _, ok := c.detail[k]; !ok {
				t.Fatalf("call %d detail %v lacks %q", i, c.detail, k)
			}
		}
	}
	want(0, "term.created", a.ID.String(), "name", "slug", "kind")

	if _, err := svc.UpdateTerm(ctx, UpdateTermInput{ID: a.ID, Name: "Alpha 2"}); err != nil {
		t.Fatal(err)
	}
	want(2, "term.updated", a.ID.String(), "name", "slug", "kind")

	if err := svc.ReplaceEntityTerms(ctx, uuid.New(), []uuid.UUID{a.ID, b.ID}); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[3]; got.action != "entity.terms_replaced" || got.detail["count"] != 2 {
		t.Fatalf("replace row = %+v", got)
	}

	if _, err := svc.MoveTerm(ctx, b.ID, MoveInput{Position: 1}); err != nil {
		t.Fatal(err)
	}
	want(4, "term.reorder", b.ID.String())

	if err := svc.MergeTerms(ctx, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	want(5, "term.merged", a.ID.String(), "into")

	if err := svc.DeleteTerm(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	want(6, "term.deleted", b.ID.String())
	if len(*calls) != 7 {
		t.Fatalf("calls = %d, want 7: %+v", len(*calls), *calls)
	}
}

func TestServiceAuditsNothingOnFailure(t *testing.T) {
	ctx := context.Background()
	svc, calls := recordedService()
	if _, err := svc.CreateTerm(ctx, CreateTermInput{Kind: "bogus kind", Name: "x"}); err == nil {
		t.Fatal("want error")
	}
	if _, err := svc.UpdateTerm(ctx, UpdateTermInput{ID: uuid.New(), Name: "x"}); err == nil {
		t.Fatal("want error")
	}
	if err := svc.ReplaceEntityTerms(ctx, uuid.New(), []uuid.UUID{uuid.New()}); err == nil {
		t.Fatal("want error")
	}
	if err := svc.MergeTerms(ctx, uuid.New(), uuid.New()); err == nil {
		t.Fatal("want error")
	}
	if len(*calls) != 0 {
		t.Fatalf("calls on failure: %+v", *calls)
	}
}

func TestServiceAuditNilHookIsSafe(t *testing.T) {
	svc := NewService(newFakeRepo(), nil)
	if _, err := svc.CreateTerm(context.Background(), CreateTermInput{Kind: "tag", Name: "x"}); err != nil {
		t.Fatal(err)
	}
}
