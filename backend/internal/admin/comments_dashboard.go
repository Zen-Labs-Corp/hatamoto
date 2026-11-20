package admin

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
)

// cdashRows is how many waiting comments the card lists; cdashExcerpt is how
// much of each body it shows.
const (
	cdashRows    = 5
	cdashExcerpt = 80
)

// commentsJob is the pending-comments card of the Overview; nil when no
// comment service is wired. It adds no KPI card. Only an owner gets the link
// that fixes the warning.
func (a *Admin) commentsJob(owner bool) *dashJob {
	if a.deps.Comments == nil {
		return nil
	}
	return &dashJob{Name: "comments", SecPos: 5, Run: func(ctx context.Context) (dashResult, error) { return a.dashComments(ctx, owner) },
		Fallback: dashSection{ID: "dash-comments", Title: "Comments", ErrMsg: "Comments could not be loaded."}}
}

// dashComments is the Comments card: the newest waiting comments and a link to
// the queue. With the site switch off and nothing waiting there is no card.
// Bounded reads: one Settings, one Counts, one Queue of cdashRows, and the
// cached site address when comments are on.
func (a *Admin) dashComments(ctx context.Context, owner bool) (dashResult, error) {
	svc := a.deps.Comments
	s, err := svc.Settings(ctx)
	if err != nil {
		return dashResult{}, err
	}
	c, err := svc.Counts(ctx)
	if err != nil {
		return dashResult{}, err
	}
	if !s.Enabled && c.Pending == 0 {
		return dashResult{}, nil
	}
	page, err := svc.Queue(ctx, CommentFilter{Status: "pending", PerPage: cdashRows})
	if err != nil {
		return dashResult{}, err
	}
	queue := a.adminPath + "/comments?status=pending"
	sec := &dashSection{ID: "dash-comments", Title: "Comments", Description: fmt.Sprintf("%d pending", c.Pending), State: dashOK,
		More: &ui.Button{Label: "Moderate", Href: queue, Variant: "ghost"}}
	loc := a.siteLocation(ctx)
	for _, r := range page.Rows {
		sec.Rows = append(sec.Rows, dashRow{Title: r.AuthorName + " on " + r.EntityTitle, Summary: truncate(strings.Join(strings.Fields(r.BodyRaw), " "), cdashExcerpt),
			When: fmtDateTime(r.CreatedAt.In(loc)), Href: queue})
	}
	if s.Enabled && a.dashNoAddress(ctx) {
		sec.Warn = "Confirmation emails are not being sent: set your site address in Settings > General."
		if owner {
			sec.WarnHref = a.adminPath + "/settings/general#identity"
		} else {
			sec.Warn = "Confirmation emails are not being sent: an owner must set the site address in Settings > General."
		}
	}
	if len(sec.Rows) == 0 {
		sec.State = dashEmpty
		sec.Empty = ui.Empty{Icon: "quote", Title: "No comments waiting", Compact: true}
	}
	return dashResult{Sec: sec}, nil
}

// dashNoAddress is commentsNoAddress for a dashboard job, which only has ctx. Deps.BaseURL reads
// only the request context (env, then the Site address setting), never headers.
func (a *Admin) dashNoAddress(ctx context.Context) bool {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	return err != nil || a.commentsNoAddress(r)
}
