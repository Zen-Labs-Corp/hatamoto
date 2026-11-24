package pluginkey

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestCommenterFromResolverRunsOnce(t *testing.T) {
	id, calls := uuid.New(), 0
	ctx := WithCommenterResolver(context.Background(), func(context.Context) (uuid.UUID, bool) {
		calls++
		return id, true
	})
	if calls != 0 {
		t.Fatal("resolver ran before a key was asked for")
	}
	for range 5 {
		if got, ok := CommenterFrom(ctx); !ok || got != id {
			t.Fatalf("CommenterFrom = %v, %v", got, ok)
		}
	}
	if calls != 1 {
		t.Errorf("resolver ran %d times, want 1", calls)
	}
}

func TestCommenterFromAbsent(t *testing.T) {
	if _, ok := CommenterFrom(context.Background()); ok {
		t.Error("no carrier must answer false")
	}
	if _, ok := CommenterFrom(WithCommenterResolver(context.Background(), nil)); ok {
		t.Error("nil resolver must answer false")
	}
	if _, ok := CommenterFrom(WithCommenter(context.Background(), uuid.Nil)); ok {
		t.Error("uuid.Nil must answer false")
	}
	if _, ok := CommenterFrom(WithCommenterResolver(context.Background(), func(context.Context) (uuid.UUID, bool) { return uuid.Nil, true })); ok {
		t.Error("a resolver answering uuid.Nil must answer false")
	}
}

func TestWithCommenterKnown(t *testing.T) {
	id := uuid.New()
	if got, ok := CommenterFrom(WithCommenter(context.Background(), id)); !ok || got != id {
		t.Errorf("CommenterFrom = %v, %v", got, ok)
	}
}
