package authzctx_test

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/authzsubject"
)

func TestFromBareContext(t *testing.T) {
	if _, ok := authzctx.From(context.Background()); ok {
		t.Fatal("a bare context must carry no subject")
	}
}

func TestWithFromRoundTrip(t *testing.T) {
	want := authzsubject.ForSession("u1", "author")
	got, ok := authzctx.From(authzctx.With(context.Background(), want))
	if !ok {
		t.Fatal("subject missing")
	}
	if got.UserID != want.UserID || got.Role != want.Role || got.KeyID != "" {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestWithoutDropsTheSubject(t *testing.T) {
	ctx := authzctx.With(context.Background(), authzsubject.ForSession("u1", "author"))
	if _, ok := authzctx.From(authzctx.Without(ctx)); ok {
		t.Fatal("Without must leave a system caller")
	}
}
