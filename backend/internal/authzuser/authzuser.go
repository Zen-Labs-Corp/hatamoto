// Package authzuser asks authz about stored users: mail recipients and
// re-checks of a looked-up user.
package authzuser

import (
	"context"
	"log/slog"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzsubject"
	"hatamoto.dev/core/internal/user"
)

// ListFunc lists users page by page, as the user repository does.
type ListFunc func(ctx context.Context, limit, offset int) ([]*user.User, error)

const page = 200

// Can reports whether u may perform act, asked as u's session. A nil user, a
// deny and an authorizer error all answer false; the error is logged.
func Can(ctx context.Context, log *slog.Logger, u *user.User, act authz.Action) bool {
	if u == nil {
		return false
	}
	ok, err := authz.Can(ctx, authzsubject.ForSession(u.ID.String(), string(u.Role)), act, authz.Object{})
	if err != nil {
		log.WarnContext(ctx, "authz check failed", "action", string(act), "err", err)
		return false
	}
	return ok
}

// Allowed pages through every user once (page rows per list call), keeps those
// keep accepts (nil keeps all) and authz allows act, in list order. A Can
// error skips that user and is logged once per call; a list error returns what
// was collected so far and the error.
func Allowed(ctx context.Context, log *slog.Logger, list ListFunc, act authz.Action, keep func(*user.User) bool) ([]*user.User, error) {
	var out []*user.User
	failed := false
	for offset := 0; ; offset += page {
		users, err := list(ctx, page, offset)
		if err != nil {
			return out, err
		}
		for _, u := range users {
			if keep != nil && !keep(u) {
				continue
			}
			ok, err := authz.Can(ctx, authzsubject.ForSession(u.ID.String(), string(u.Role)), act, authz.Object{})
			if err != nil {
				if !failed {
					log.WarnContext(ctx, "authz check failed", "action", string(act), "err", err)
					failed = true
				}
				continue
			}
			if ok {
				out = append(out, u)
			}
		}
		if len(users) < page {
			return out, nil
		}
	}
}

// Emails lists the address of every user in order; empty ones are included,
// callers decide what to do with them.
func Emails(us []*user.User) []string {
	out := make([]string, 0, len(us))
	for _, u := range us {
		out = append(out, u.Email)
	}
	return out
}
