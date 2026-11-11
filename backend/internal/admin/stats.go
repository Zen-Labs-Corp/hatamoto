package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
)

// Install stats levels, as the service and the forms name them.
const (
	statsLevelUnset    = "unset"
	statsLevelOff      = "off"
	statsLevelBasic    = "basic"
	statsLevelDetailed = "detailed"
)

// Banner kinds: what the owner is asked on every page.
const (
	statsBannerUnanswered = "unanswered"
	statsBannerNudge      = "nudge"
	statsBannerDefaulted  = "defaulted"
)

// statsDocsURL is the page that says what each level collects.
const statsDocsURL = "https://hatamoto.dev/docs/operate/install-stats"

// StatsService is the install stats as Settings > Updates, the setup
// wizard and the owner banner use it. The server implements it on top of the
// installstats package; the admin only validates forms and calls through.
type StatsService interface {
	// View is the stored level and whether the server configuration forces it
	// off.
	View(ctx context.Context) (StatsView, error)
	// Banner is the question to put to the owner: "unanswered" (the level is
	// unset), "defaulted" (Detailed was applied by default, notice not yet
	// acknowledged), "nudge" (on Basic, Detailed not yet offered) or "" for none. It
	// must be cheap, the banner asks it on every owner page.
	Banner(ctx context.Context) (string, error)
	// ForcedOff reports HATAMOTO_TELEMETRY=off. It reads no state.
	ForcedOff() bool
	// Choose stores the owner's answer: "off", "basic" or "detailed".
	Choose(ctx context.Context, level string) error
	// DismissNudge stores that the owner does not want the Detailed nudge.
	DismissNudge(ctx context.Context) error
	// ApplyDefault stores the Detailed default for an install that was never
	// asked, once setup is complete. It is a no-op when the server forbids it.
	ApplyDefault(ctx context.Context) error
	// Reset replaces the install ID with a new one.
	Reset(ctx context.Context) error
}

// StatsView is what the admin shows about install stats.
type StatsView struct {
	Level     string     // "unset", "off", "basic" or "detailed"
	ForcedOff bool       // HATAMOTO_TELEMETRY=off: the owner cannot change it
	LastSent  *time.Time // time of the last successful report
}

// statsOn reports whether a report is sent at the level.
func statsOn(level string) bool { return level == statsLevelBasic || level == statsLevelDetailed }

func validStatsLevel(level string) bool {
	return level == statsLevelOff || statsOn(level)
}

// statsLevelCards are the three level choices of the settings card and the
// wizard; checked is the level to preselect, "" for none.
func statsLevelCards(checked string) []ui.RadioCardItem {
	return []ui.RadioCardItem{
		{Value: statsLevelOff, Title: "Off", Description: "Nothing is sent", Checked: checked == statsLevelOff},
		{Value: statsLevelBasic, Title: "Basic", Description: "Version and update channel", Checked: checked == statsLevelBasic},
		{Value: statsLevelDetailed, Title: "Detailed", Description: "Basic, plus plugins, theme and error counts", Checked: checked == statsLevelDetailed},
	}
}

// SetStats sets the install stats service after New, like SetUpdates. Call it
// only with a non-nil service: a typed nil would defeat the nil checks.
func (a *Admin) SetStats(svc StatsService) {
	a.deps.Stats = svc
}

const statsForcedOffNote = "Turned off by the server configuration."

// Store errors are never shown: they are logged at Warn and the owner goes on
// (a quiet redirect, no flash, no audit entry) and can simply try again.
func (a *Admin) statsQuietRedirect(w http.ResponseWriter, r *http.Request, msg string, err error) {
	a.logger().Warn(msg, "err", err)
	http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
}

// statsView loads the stats view for a POST handler. It answers the request
// itself and returns false when the service is missing or the store fails.
func (a *Admin) statsView(w http.ResponseWriter, r *http.Request) (StatsView, bool) {
	if a.deps.Stats == nil {
		a.unavailable(w, r, "Install stats")
		return StatsView{}, false
	}
	v, err := a.deps.Stats.View(r.Context())
	if err != nil {
		a.statsQuietRedirect(w, r, "stats view", err)
		return StatsView{}, false
	}
	return v, true
}

// statsChoose applies a level. Under a forced-off server it changes nothing.
// The caller has validated the level.
func (a *Admin) statsChoose(w http.ResponseWriter, r *http.Request, level string) {
	v, ok := a.statsView(w, r)
	if !ok {
		return
	}
	if v.ForcedOff {
		a.SetFlash(w, r, "info", statsForcedOffNote)
		http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
		return
	}
	if err := a.deps.Stats.Choose(r.Context(), level); err != nil {
		a.statsQuietRedirect(w, r, "stats choose", err)
		return
	}
	a.audit(r, "stats.consent", "stats", map[string]any{"choice": level})
	if statsOn(level) {
		a.SetFlash(w, r, "success", "Install stats are on: "+strings.ToUpper(level[:1])+level[1:]+".")
	} else {
		a.SetFlash(w, r, "success", "Install stats are off.")
	}
	http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
}

// handleStatsLevel is the Settings > Updates radio group: level=off|basic|detailed.
// Anything else is refused, never read as a default.
func (a *Admin) handleStatsLevel(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	level := r.PostFormValue("level")
	if !validStatsLevel(level) {
		a.Error(w, r, http.StatusBadRequest, "Choose Off, Basic or Detailed", "Pick one of the three levels.")
		return
	}
	a.statsChoose(w, r, level)
}

// handleStatsChoice is the banner answer: choice=off|basic|detailed. Anything
// else is refused, never read as a default.
func (a *Admin) handleStatsChoice(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	choice := r.PostFormValue("choice")
	if !validStatsLevel(choice) {
		a.Error(w, r, http.StatusBadRequest, "Choose Off, Basic or Detailed", "Pick one of the three answers.")
		return
	}
	a.statsChoose(w, r, choice)
}

// handleStatsDismiss stores that the owner does not want the Detailed nudge.
func (a *Admin) handleStatsDismiss(w http.ResponseWriter, r *http.Request) {
	v, ok := a.statsView(w, r)
	if !ok {
		return
	}
	if v.ForcedOff {
		a.SetFlash(w, r, "info", statsForcedOffNote)
		http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
		return
	}
	if err := a.deps.Stats.DismissNudge(r.Context()); err != nil {
		a.statsQuietRedirect(w, r, "stats dismiss", err)
		return
	}
	http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
}

func (a *Admin) handleStatsReset(w http.ResponseWriter, r *http.Request) {
	v, ok := a.statsView(w, r)
	if !ok {
		return
	}
	if v.ForcedOff {
		a.SetFlash(w, r, "info", statsForcedOffNote)
		http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
		return
	}
	if err := a.deps.Stats.Reset(r.Context()); err != nil {
		a.statsQuietRedirect(w, r, "stats reset", err)
		return
	}
	a.audit(r, "stats.reset", "stats", nil)
	a.SetFlash(w, r, "success", "Install ID reset.")
	http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
}

// StatsBanner is the owner's question about install stats: the first answer
// ("unanswered"), the one-time notice that Detailed was applied by default
// ("defaulted") or the one-time offer of Detailed ("nudge").
type StatsBanner struct {
	Kind    string // statsBannerUnanswered, statsBannerDefaulted or statsBannerNudge
	DocsURL string
}

// statsBanner is the banner for a page view: nil unless the owner is on a GET
// page of a finished setup and the service has a question for them (never
// under a forced-off server). Editors never reach the service, and the Updates
// pages already show the choice as their Install stats card. The answer is a
// POST (see handleStatsChoice, handleStatsDismiss), not a browser-side
// dismissal.
func (a *Admin) statsBanner(r *http.Request, u *CurrentUser, setupPending bool) *StatsBanner {
	if a.deps.Stats == nil || a.deps.Demo.Enabled || u == nil || u.Role != "owner" || setupPending {
		return nil
	}
	if strings.HasPrefix(r.URL.Path, a.adminPath+updatesPath) {
		return nil
	}
	kind, err := a.deps.Stats.Banner(r.Context())
	if err != nil || (kind != statsBannerUnanswered && kind != statsBannerNudge && kind != statsBannerDefaulted) {
		return nil
	}
	return &StatsBanner{Kind: kind, DocsURL: statsDocsURL}
}
