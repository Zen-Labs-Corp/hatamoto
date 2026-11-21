package admin

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzsubject"
	"hatamoto.dev/core/internal/user"
)

// canUser reports whether u may perform act. No user, a deny and an
// authorizer error all answer false; the error is logged.
func canUser(ctx context.Context, log *slog.Logger, u *CurrentUser, act authz.Action) bool {
	return canUserObj(ctx, log, u, act, authz.Object{})
}

// pluginObject is the object of an action on the plugin called name.
func pluginObject(name string) authz.Object { return authz.Object{Kind: "plugin", ID: name} }

// canUserObj is canUser for an action on obj.
func canUserObj(ctx context.Context, log *slog.Logger, u *CurrentUser, act authz.Action, obj authz.Object) bool {
	if u == nil {
		return false
	}
	ok, err := authz.Can(ctx, authzsubject.ForSession(u.ID, u.Role), act, obj)
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

// canObj is can for an action on obj.
func (a *Admin) canObj(ctx context.Context, act authz.Action, obj authz.Object) bool {
	return canUserObj(ctx, a.logger(), userFrom(ctx), act, obj)
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

// requireOwnerGrant answers 403 and reports false when role is owner and the
// signed-in user may not grant it (user.owner.grant), as on the REST user paths.
// Any other role needs no extra check; an authorizer error is a deny.
func (a *Admin) requireOwnerGrant(w http.ResponseWriter, r *http.Request, role user.Role) bool {
	if role != user.RoleOwner || a.can(r.Context(), authz.ActionUserOwnerGrant) {
		return true
	}
	a.Error(w, r, http.StatusForbidden, "Not allowed", "Only owners can grant the owner role.")
	return false
}

// viewer is the signed-in user as nav, tabs and flags see them. Role is only
// for SDK plugin allow-lists (uiAllows); every core decision uses Can.
type viewer struct {
	Role string
	can  func(authz.Action, authz.Object) bool
}

// Can reports whether the viewer may perform act; the zero viewer may not.
func (v viewer) Can(act authz.Action) bool { return v.CanObj(act, authz.Object{}) }

// CanObj reports whether the viewer may perform act on obj.
func (v viewer) CanObj(act authz.Action, obj authz.Object) bool {
	return v.can != nil && v.can(act, obj)
}

// newViewer builds the viewer of u. It asks authz once per action and object: nav asks
// the same actions many times per render. A nil u gives the zero viewer.
func newViewer(ctx context.Context, log *slog.Logger, u *CurrentUser) viewer {
	if u == nil {
		return viewer{}
	}
	var mu sync.Mutex
	type key struct {
		act authz.Action
		obj authz.Object
	}
	memo := map[key]bool{}
	return viewer{Role: u.Role, can: func(act authz.Action, obj authz.Object) bool {
		mu.Lock()
		defer mu.Unlock()
		if ok, seen := memo[key{act, obj}]; seen {
			return ok
		}
		ok := canUserObj(ctx, log, u, act, obj)
		memo[key{act, obj}] = ok
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
