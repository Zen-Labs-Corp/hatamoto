package admin

import (
	"net/http"
	"strings"
	"time"
)

// updateChipMax is how many release blocks the top-bar chip and its history
// list show.
const updateChipMax = 6

// UpdateChip is the owner's always-visible update status in the top bar: a few
// tiny release blocks, the running version and a popover with the latest
// releases. Built from the cached UpdatesGlance, never from View.
type UpdateChip struct {
	ID      string // the popover's element id
	Version string // "v1.4.2", or "dev build"
	Title   string // "Up to date", "v1.4.3 is ready", "v1.4.3 rolled back"
	Channel string // "Stable channel", "" on a dev build
	Note    string // one sentence under the title (a major release, updates off for 90+ days)
	Name    string // the trigger's accessible name
	Blocks  []UpdateChipBlock
	Rows    []UpdateChipRow
	Href    string
	Label   string
}

// UpdateChipBlock is one tiny release block, oldest first.
type UpdateChipBlock struct {
	Kind string // success | danger | info | neutral
}

// UpdateChipRow is one release switch in the popover.
type UpdateChipRow struct {
	Text  string // "Updated to v1.4.2"
	When  string
	Badge string // badge kind
}

// chipVersion shows a version with a single leading v.
func chipVersion(v string) string {
	if v = strings.TrimPrefix(strings.TrimSpace(v), "v"); v == "" {
		return ""
	}
	return "v" + v
}

// buildUpdateChip turns a glance into the chip. prefix is the admin path.
func buildUpdateChip(g UpdatesGlance, prefix string, loc *time.Location, now time.Time) *UpdateChip {
	c := &UpdateChip{
		ID: "hm-update-chip", Href: prefix + updatesPath, Label: "Update settings",
		Version: chipVersion(g.Running),
	}
	if g.Dev {
		c.Version, c.Title = "dev build", "Development build"
		c.Note = "Updates are disabled in development builds."
		c.Name = c.Version + ", Development build. Update history"
		return c
	}
	c.Channel = chipChannel(g.Channel)
	cur := UpdatesState{Running: g.Running, Dev: g.Dev, Mode: g.Mode, Status: g.Status}.Current()
	if cur.Decision == updMajor && chipVersion(cur.AvailableMajor) == c.Version {
		cur.Decision = updUpToDate // a stale major status for the release already running
	}
	if t := chipReady(cur); g.Applying && t != "" {
		cur.Decision, cur.Target = updApplying, t // the saved status lags a run that has just started
	}
	c.Title = chipTitle(cur)
	switch {
	case cur.Decision == updMajor:
		c.Note = "A major release can change how data is stored. Review it in Update settings."
	case g.Mode == "off" && g.OffSince != nil && now.Sub(*g.OffSince) >= offBannerAfter:
		c.Note = "Automatic updates have been off for 90+ days."
	}

	hist := g.History
	if len(hist) > updateChipMax {
		hist = hist[:updateChipMax]
	}
	ready := chipReady(cur)
	blocks := hist
	if ready != "" && len(blocks) == updateChipMax {
		blocks = blocks[:updateChipMax-1]
	}
	for i := len(blocks) - 1; i >= 0; i-- {
		c.Blocks = append(c.Blocks, UpdateChipBlock{Kind: updatesHistoryBadge(blocks[i]).Kind})
	}
	if ready != "" {
		c.Blocks = append(c.Blocks, UpdateChipBlock{Kind: "info"})
	}
	for _, h := range hist {
		b := updatesHistoryBadge(h)
		text := b.Label
		if h.To != "" {
			text += " " + chipVersion(h.To)
		}
		c.Rows = append(c.Rows, UpdateChipRow{Text: text, When: fmtDate(h.When.In(loc)), Badge: b.Kind})
	}
	// The name starts with the visible text, so "click v1.4.2" works by voice.
	c.Name = c.Version + ", " + c.Title + ". Update history"
	if c.Version == "" {
		c.Name = c.Title + ". Update history"
	}
	return c
}

// chipReady is the version waiting to install, "" when none.
func chipReady(st UpdatesStatus) string {
	switch st.Decision {
	case updMajor:
		return st.AvailableMajor
	case updScheduled, updWaitingWindow, updWaitingDefer, updStaged, updNotify, updApplying:
		return st.Target
	}
	return ""
}

// chipTitle is the popover heading for the current status.
func chipTitle(st UpdatesStatus) string {
	switch v := chipVersion(chipReady(st)); {
	case st.Decision == updMajor && v != "":
		return "Major update " + v + " is ready"
	case st.Decision == updApplying && v != "":
		return "Updating to " + v
	case v != "":
		return v + " is ready"
	case st.Decision == updRolledBack && st.Target != "":
		return chipVersion(st.Target) + " rolled back"
	}
	return updatesStatusBadge(st, false).Label
}

// chipChannel is the short channel name ("Stable channel").
func chipChannel(c string) string {
	if c == "" {
		return ""
	}
	return strings.ToUpper(c[:1]) + c[1:] + " channel"
}

// updateChip is the chip for a page view: nil unless the owner is on a GET
// page and the site has an update service. Editors never reach the service.
func (a *Admin) updateChip(r *http.Request, u *CurrentUser) *UpdateChip {
	if a.deps.Updates == nil || u == nil || u.Role != "owner" {
		return nil
	}
	g, err := a.deps.Updates.Glance(r.Context())
	if err != nil {
		return nil
	}
	return buildUpdateChip(g, a.adminPath, a.siteLocation(r.Context()), updatesNow())
}
