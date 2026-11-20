package admin

import (
	"context"
	"fmt"
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
// comment service is wired. It adds no KPI card.
func (a *Admin) commentsJob() *dashJob {
	if a.deps.Comments == nil {
		return nil
	}
	return &dashJob{Name: "comments", SecPos: 5, Run: a.dashComments,
		Fallback: dashSection{ID: "dash-comments", Title: "Comments", ErrMsg: "Comments could not be loaded."}}
}

// dashComments is the Comments card: the newest waiting comments and a link to
// the queue. With the site switch off and nothing waiting there is no card.
// Bounded reads: one Settings, one Counts, one Queue of cdashRows.
func (a *Admin) dashComments(ctx context.Context) (dashResult, error) {
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
	if len(sec.Rows) == 0 {
		sec.State = dashEmpty
		sec.Empty = ui.Empty{Icon: "quote", Title: "No comments waiting", Compact: true}
	}
	return dashResult{Sec: sec}, nil
}
