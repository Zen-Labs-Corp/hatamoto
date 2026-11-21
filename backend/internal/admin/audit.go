package admin

import (
	"context"
	"net/http"
	"time"

	"hatamoto.dev/core/internal/middleware"
)

// AuditEvent is one owner action for the admin audit log. Detail must carry
// no secrets.
type AuditEvent struct {
	ActorID, Actor, Action, Target string
	Detail                         map[string]any
	IP, UserAgent, RequestID       string // set from the admin request
}

// audit records an action by the signed-in user; it does nothing when no sink
// is wired.
func (a *Admin) audit(r *http.Request, action, target string, detail map[string]any) {
	if a.deps.Audit == nil {
		return
	}
	e := AuditEvent{
		Action: action, Target: target, Detail: detail,
		IP: middleware.GetClientIP(r), UserAgent: r.UserAgent(), RequestID: r.Header.Get("X-Request-ID"),
	}
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
	Src                   string // "a" admin table, "m" agent calls: the row id prefix and the cursor tie-break
	Source                string // admin|api|agent|cli|system; set by Search only
	Result                string // ok|denied|error; set by Search only
	Error                 string // agent call error text; set by Search only
}

// AuditReader lists stored audit events newest first. Recent also counts the
// stored events (for pagination); Latest skips that count for callers that
// only want the newest few. Search reads admin actions and agent calls
// together with filters and keyset paging; Facets lists the actors and
// actions seen in a date window for the filter selects.
type AuditReader interface {
	Recent(ctx context.Context, limit, offset int) ([]AuditRow, int, error)
	Latest(ctx context.Context, limit int) ([]AuditRow, error)
	Search(ctx context.Context, q AuditQuery) (AuditPage, error)
	Facets(ctx context.Context, from, to time.Time) (AuditFacets, error)
}
