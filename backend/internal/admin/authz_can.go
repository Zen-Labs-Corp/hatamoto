package admin

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzsubject"
)

// canUser reports whether u may perform act. No user, a deny and an
// authorizer error all answer false; the error is logged.
func canUser(ctx context.Context, log *slog.Logger, u *CurrentUser, act authz.Action) bool {
	if u == nil {
		return false
	}
	ok, err := authz.Can(ctx, authzsubject.ForSession(u.ID, u.Role), act, authz.Object{})
	if err != nil {
		log.WarnContext(ctx, "authz check failed", "action", string(act), "err", err)
		return false
	}
	return ok
}

// can reports whether the signed-in user of the request may perform act.
func (a *Admin) can(ctx context.Context, act authz.Action) bool {
	return canUser(ctx, a.logger(), userFrom(ctx), act)
}

// requireCan answers 403 (the same page as forbidden) and reports false
// unless the signed-in user may perform act.
func (a *Admin) requireCan(w http.ResponseWriter, r *http.Request, act authz.Action) bool {
	if a.can(r.Context(), act) {
		return true
	}
	a.forbidden(w, r)
	return false
}

// viewer is the signed-in user as nav, tabs and flags see them. Role is only
// for SDK plugin allow-lists (uiAllows); every core decision uses Can.
type viewer struct {
	Role string
	can  func(authz.Action) bool
}

// Can reports whether the viewer may perform act; the zero viewer may not.
func (v viewer) Can(act authz.Action) bool { return v.can != nil && v.can(act) }

// newViewer builds the viewer of u. It asks authz once per action: nav asks
// the same actions many times per render. A nil u gives the zero viewer.
func newViewer(ctx context.Context, log *slog.Logger, u *CurrentUser) viewer {
	if u == nil {
		return viewer{}
	}
	var mu sync.Mutex
	memo := map[authz.Action]bool{}
	return viewer{Role: u.Role, can: func(act authz.Action) bool {
		mu.Lock()
		defer mu.Unlock()
		if ok, seen := memo[act]; seen {
			return ok
		}
		ok := canUser(ctx, log, u, act)
		memo[act] = ok
		return ok
	}}
}

// viewerOf is the viewer of the signed-in user of the request.
func (a *Admin) viewerOf(ctx context.Context) viewer {
	return newViewer(ctx, a.logger(), userFrom(ctx))
}

// viewer is the viewer of the page; a View built without NewView (tests)
// falls back to one made from its User.
func (v *View) viewer() viewer {
	if v.vw.can != nil || v.User == nil {
		return v.vw
	}
	return newViewer(context.Background(), slog.Default(), v.User)
}
