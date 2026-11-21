package middleware

import (
	"context"
	"log/slog"

	"hatamoto.dev/core/authz"
)

// Can reports whether the request's subject (API key first, then the
// session) may perform act. No subject, a deny and an authorizer error all
// answer false; the error is logged.
func Can(ctx context.Context, act authz.Action, obj authz.Object) bool {
	s, status := requestSubject(ctx)
	if status != 0 {
		return false
	}
	ok, err := authz.Can(ctx, s, act, obj)
	if err != nil {
		slog.WarnContext(ctx, "authz check failed", "action", string(act), "err", err)
		return false
	}
	return ok
}
