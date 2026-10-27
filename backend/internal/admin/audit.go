package admin

import (
	"context"
	"net/http"
	"time"
)

// AuditEvent is one owner action for the admin audit log. Detail must carry
// no secrets.
type AuditEvent struct {
	ActorID, Actor, Action, Target string
	Detail                         map[string]any
}

// audit records an action by the signed-in user; it does nothing when no sink
// is wired.
func (a *Admin) audit(r *http.Request, action, target string, detail map[string]any) {
	if a.deps.Audit == nil {
		return
	}
	e := AuditEvent{Action: action, Target: target, Detail: detail}
	if u := userFrom(r.Context()); u != nil {
		e.ActorID, e.Actor = u.ID, u.Email
	}
	a.deps.Audit(r.Context(), e)
}

// AuditRow is one stored audit event as the Audit log page and the Overview
// Activity card read it.
type AuditRow struct {
	ID                    int64
	At                    time.Time
	Actor, Action, Target string
	Detail                map[string]any
}

// AuditReader lists stored audit events newest first. Recent also counts the
// stored events (for pagination); Latest skips that count for callers that
// only want the newest few.
type AuditReader interface {
	Recent(ctx context.Context, limit, offset int) ([]AuditRow, int, error)
	Latest(ctx context.Context, limit int) ([]AuditRow, error)
}
