package taxonomy

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type mergingRepo struct{ *fakeRepo }

func (mergingRepo) MergeTerms(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func TestMergeTermsNotifiesChangeObserver(t *testing.T) {
	svc := NewService(mergingRepo{newFakeRepo()}, nil)
	calls := 0
	svc.SetChangeObserver(func(context.Context) { calls++ })
	ctx := context.Background()
	a, err := svc.CreateTerm(ctx, CreateTermInput{Kind: "tag", Name: "A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.CreateTerm(ctx, CreateTermInput{Kind: "tag", Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	calls = 0
	if err := svc.MergeTerms(ctx, a.ID, b.ID); err != nil || calls != 1 {
		t.Fatalf("merge: err=%v calls=%d", err, calls)
	}
	if err := svc.MergeTerms(ctx, a.ID, a.ID); err == nil || calls != 1 {
		t.Fatalf("refused merge: err=%v calls=%d", err, calls)
	}
}
