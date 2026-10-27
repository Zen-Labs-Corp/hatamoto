package render

import (
	"context"
	"testing"
)

// A scope sees failures noted on its own goroutine or via its context, never
// those of another goroutine.
func TestDegradeScopeIsPerRequest(t *testing.T) {
	ctx, s, release := BindDegradeScope(context.Background())
	defer release()
	done := make(chan struct{})
	go func() { NoteDegraded(); close(done) }()
	<-done
	if s.Count() != 0 {
		t.Fatalf("another goroutine's failure counted: %d", s.Count())
	}
	NoteDegraded() // a template func on the render goroutine
	NoteDegradedCtx(ctx)
	if s.Count() != 2 {
		t.Fatalf("own failures = %d, want 2", s.Count())
	}
}
