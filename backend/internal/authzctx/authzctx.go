// Package authzctx carries the authorization subject of a request to the services, which check objects
// (an entity's creator and status) that only they load. No subject means a system caller.
package authzctx

import (
	"context"

	"hatamoto.dev/core/authz"
)

type key struct{}

// With returns ctx carrying s as the subject of the work done under it.
func With(ctx context.Context, s authz.Subject) context.Context {
	return context.WithValue(ctx, key{}, s)
}

// From returns the subject on ctx. The bool is false for a system caller (no subject).
func From(ctx context.Context) (authz.Subject, bool) {
	s, ok := ctx.Value(key{}).(authz.Subject)
	return s, ok
}
