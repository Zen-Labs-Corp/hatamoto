package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzsubject"
)

// requestSubject describes who made the request. A request made with an API
// key is always a key subject: keys run with synthesised editor claims, which
// must never be read as a session. The second value is the HTTP status to
// answer with when there is no usable subject, or 0.
func requestSubject(ctx context.Context) (authz.Subject, int) {
	if key, ok := GetAPIKey(ctx); ok {
		return authzsubject.ForKey(key), 0
	}
	if GetAuthType(ctx) == AuthTypeAPIKey {
		return authz.Subject{}, http.StatusForbidden
	}
	c, ok := GetClaims(ctx)
	if !ok {
		return authz.Subject{}, http.StatusUnauthorized
	}
	return authzsubject.ForSession(c.UserID.String(), c.Role), 0
}

// defaultRoles lists the session roles the built-in authorizer admits for act.
// It only feeds the trace tag; a mount-time unknown action is a programming
// error and panics.
func defaultRoles(act authz.Action) []string {
	roles := []string{"owner"}
	ok, err := authz.Default().Can(context.Background(), authzsubject.ForSession("trace", "editor"), act, authz.Object{})
	if err != nil {
		panic(fmt.Sprintf("middleware: %v", err))
	}
	if ok {
		roles = append(roles, "editor")
	}
	return roles
}

// deny answers a failed gate with the REST error bodies.
func deny(w http.ResponseWriter, status int) {
	if status == http.StatusUnauthorized {
		http.Error(w, `{"error":"unauthorized"}`, status)
		return
	}
	http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
}

// allowed asks authz and fails closed on an error.
func allowed(r *http.Request, s authz.Subject, act authz.Action) bool {
	ok, err := authz.Can(r.Context(), s, act, authz.Object{})
	if err != nil {
		slog.WarnContext(r.Context(), "authz check failed", "action", act, "err", err)
		return false
	}
	return ok
}
