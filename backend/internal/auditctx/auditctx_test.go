package auditctx

import (
	"context"
	"testing"
)

func TestWithFromRoundTrip(t *testing.T) {
	t.Parallel()

	want := Actor{ID: "u1", Name: "a@b.c", Kind: "user", Via: "api", Detail: map[string]any{"schema": "page"}}
	got, ok := From(With(context.Background(), want))
	if !ok || got.ID != want.ID || got.Name != want.Name || got.Via != "api" || got.Detail["schema"] != "page" {
		t.Fatalf("From = %+v, %v", got, ok)
	}
}

func TestFromWithoutActor(t *testing.T) {
	t.Parallel()

	if _, ok := From(context.Background()); ok {
		t.Fatal("empty context has an actor")
	}
	if _, ok := From(With(context.Background(), Actor{Name: "x"})); ok {
		t.Fatal("an actor without a Kind must not count")
	}
}
