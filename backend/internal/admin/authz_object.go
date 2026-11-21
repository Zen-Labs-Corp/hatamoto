package admin

import (
	"context"
	"errors"
	"net/http"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/media"
)

// canEntity reports whether the signed-in user may perform act on e.
func (a *Admin) canEntity(ctx context.Context, act authz.Action, e *entity.Entity) bool {
	return a.canObj(ctx, act, e.AuthzObject())
}

// canMedia reports whether the signed-in user may perform act on m.
func (a *Admin) canMedia(ctx context.Context, act authz.Action, m *media.Media) bool {
	return a.canObj(ctx, act, m.AuthzObject())
}

// refuseNotAllowed answers the standard 403 (the error page, or the fetch JSON with the refusal sentence) when err
// is entity.ErrNotAllowed, and reports whether it did.
func (a *Admin) refuseNotAllowed(w http.ResponseWriter, r *http.Request, err error) bool {
	if !errors.Is(err, entity.ErrNotAllowed) {
		return false
	}
	msg := sentence(err.Error())
	if a.fetchError(w, r, http.StatusForbidden, msg) {
		return true
	}
	a.Error(w, r, http.StatusForbidden, "Not allowed", msg)
	return true
}
