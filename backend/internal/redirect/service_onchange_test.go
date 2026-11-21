package redirect

import (
	"context"
	"testing"
)

func TestOnChangeFiresOnWritesOnly(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store, nil)
	calls := 0
	s.SetOnChange(func() { calls++ })
	ctx := context.Background()

	rd, err := s.CreateRedirect(ctx, &CreateRedirectInput{SourcePath: "/old", TargetURL: "/new", StatusCode: 301})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("after create: %d calls, want 1", calls)
	}
	if _, err := s.UpdateRedirect(ctx, rd.ID, &UpdateRedirectInput{}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("after update: %d calls, want 2", calls)
	}
	if err := s.DeleteRedirect(ctx, rd.ID); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("after delete: %d calls, want 3", calls)
	}
	s.InvalidateCache() // writers that bypass the service (the importer)
	if calls != 4 {
		t.Fatalf("after InvalidateCache: %d calls, want 4", calls)
	}

	// Reads do not fire it.
	before := calls
	_, _ = s.GetRedirect(ctx, rd.ID)
	s.CheckRedirect(ctx, "/old")
	if calls != before {
		t.Errorf("a read fired the listener (%d calls)", calls-before)
	}
}

func TestOnChangeUnsetIsHarmless(t *testing.T) {
	s := newTestService(t, newFakeStore(), nil)
	s.InvalidateCache()
}
