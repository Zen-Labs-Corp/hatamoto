package admin

import (
	"context"
	"sort"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
)

// dashActivityN is how many events the Activity card lists.
const dashActivityN = 6

// autoUpdateActor is who an update install or rollback is shown as: the
// updater runs without a request, so no person is recorded.
const autoUpdateActor = "Auto update"

// activityEvent is one line of the card, from either source.
type activityEvent struct {
	At          time.Time
	Who, Target string
	Badge       ui.Badge
}

// activityRow is a line of the card as the template draws it.
type activityRow struct {
	Who, Target, When string
	Badge             ui.Badge
}

// auditEvents are the newest audit rows as card lines.
func auditEvents(rows []AuditRow) []activityEvent {
	out := make([]activityEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, activityEvent{At: r.At, Who: r.Actor, Target: auditTarget(r), Badge: ui.Badge{Label: auditLabel(r.Action), Plain: true}})
	}
	return out
}

// updateEvents are the update history rows as card lines.
func updateEvents(rows []UpdatesHistoryRow) []activityEvent {
	out := make([]activityEvent, 0, len(rows))
	for _, h := range rows {
		out = append(out, activityEvent{At: h.When, Who: autoUpdateActor, Target: chipVersion(h.To), Badge: updatesHistoryBadge(h)})
	}
	return out
}

// mergeActivity is the newest n events of the lists, newest first. Events with
// the same time keep the order of the lists they came in.
func mergeActivity(n int, lists ...[]activityEvent) []activityEvent {
	var all []activityEvent
	for _, l := range lists {
		all = append(all, l...)
	}
	sort.SliceStable(all, func(i, k int) bool { return all[i].At.After(all[k].At) })
	if len(all) > n {
		all = all[:n]
	}
	return all
}

// dashActivity is the Activity card: the newest admin changes and update
// installs. One source failing leaves the other; both failing is an error.
func (a *Admin) dashActivity(ctx context.Context) (dashResult, error) {
	var audit, updates []activityEvent
	var aerr, uerr error
	if a.deps.AuditLog != nil {
		var rows []AuditRow
		if rows, aerr = a.deps.AuditLog.Latest(ctx, dashActivityN); aerr == nil {
			audit = auditEvents(rows)
		}
	}
	if a.deps.Updates != nil {
		var g UpdatesGlance
		if g, uerr = a.deps.Updates.Glance(ctx); uerr == nil {
			updates = updateEvents(g.History[:min(len(g.History), dashActivityN)])
		}
	}
	if aerr != nil && (uerr != nil || a.deps.Updates == nil) {
		return dashResult{}, aerr
	}
	if uerr != nil && a.deps.AuditLog == nil {
		return dashResult{}, uerr
	}
	for _, err := range []error{aerr, uerr} {
		if err != nil {
			a.logger().Warn("admin: one activity source failed", "err", err)
		}
	}
	sec := &dashSection{ID: "dash-activity", Title: "Activity", Description: "The latest changes in the admin, newest first.", State: dashOK}
	if a.deps.AuditLog != nil {
		sec.More = &ui.Button{Label: "Audit log", Href: a.adminPath + auditRel, Variant: "ghost"}
	}
	events := mergeActivity(dashActivityN, audit, updates)
	if len(events) == 0 {
		sec.State = dashEmpty
		sec.Empty = ui.Empty{Icon: "clock", Title: "No activity yet", Body: "Changes made in the admin and updates will show here.", Compact: true}
		return dashResult{Sec: sec}, nil
	}
	loc := a.siteLocation(ctx)
	for _, e := range events {
		sec.Activity = append(sec.Activity, activityRow{Who: e.Who, Badge: e.Badge, Target: e.Target, When: fmtDateTime(e.At.In(loc))})
	}
	return dashResult{Sec: sec}, nil
}

// activityJob is the Activity card, owner only; nil when neither source is wired.
func (a *Admin) activityJob() *dashJob {
	if a.deps.AuditLog == nil && a.deps.Updates == nil {
		return nil
	}
	return &dashJob{Name: "activity", SecPos: 5, Run: a.dashActivity,
		Fallback: dashSection{ID: "dash-activity", Title: "Activity", Description: "The latest changes in the admin, newest first.", ErrMsg: "The activity could not be read."}}
}
