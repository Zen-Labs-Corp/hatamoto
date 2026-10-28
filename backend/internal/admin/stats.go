package admin

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// StatsService is the opt-in install stats as Settings > Updates, the setup
// wizard and the owner banner use it. The server implements it on top of the
// installstats package; the admin only validates forms and calls through.
type StatsService interface {
	// View is the stored choice, whether the server configuration forces it
	// off, and the exact payload the daily ping would send.
	View(ctx context.Context) (StatsView, error)
	// Unanswered reports whether the owner still has to answer: the server
	// configuration does not turn stats off and the choice is unset. It must
	// be cheap, the banner asks it on every owner page.
	Unanswered(ctx context.Context) (bool, error)
	// ForcedOff reports HATAMOTO_TELEMETRY=off. It reads no state.
	ForcedOff() bool
	// Choose stores the owner's answer. Yes creates the install ID, No (or
	// switching off) deletes it.
	Choose(ctx context.Context, yes bool) error
	// Reset replaces the install ID with a new one.
	Reset(ctx context.Context) error
}

// StatsView is what the admin shows about install stats.
type StatsView struct {
	Consent     string     // "unset", "yes" or "no"
	ForcedOff   bool       // HATAMOTO_TELEMETRY=off: the owner cannot change it
	PreviewJSON string     // the exact request body
	LastSent    *time.Time // time of the last successful report
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

// statsChoose applies yes or no. Under a forced-off server it changes nothing.
func (a *Admin) statsChoose(w http.ResponseWriter, r *http.Request, yes bool) {
	v, ok := a.statsView(w, r)
	if !ok {
		return
	}
	if v.ForcedOff {
		a.SetFlash(w, r, "info", statsForcedOffNote)
		http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
		return
	}
	if err := a.deps.Stats.Choose(r.Context(), yes); err != nil {
		a.statsQuietRedirect(w, r, "stats choose", err)
		return
	}
	choice := "no"
	if yes {
		choice = "yes"
	}
	a.audit(r, "stats.consent", "stats", map[string]any{"choice": choice})
	if yes {
		a.SetFlash(w, r, "success", "Install stats are on.")
	} else {
		a.SetFlash(w, r, "success", "Install stats are off.")
	}
	http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
}

// handleStatsSwitch is the Settings > Updates switch: stats=on or stats=off
// (an unchecked switch posts the off value).
func (a *Admin) handleStatsSwitch(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	a.statsChoose(w, r, r.PostFormValue("stats") == "on")
}

// handleStatsChoice is the banner and wizard answer: choice=yes or choice=no.
// Anything else is refused, never read as a default.
func (a *Admin) handleStatsChoice(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	switch r.PostFormValue("choice") {
	case "yes":
		a.statsChoose(w, r, true)
	case "no":
		a.statsChoose(w, r, false)
	default:
		a.Error(w, r, http.StatusBadRequest, "Choose Yes or No", "Pick one of the two answers.")
	}
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

// StatsBanner is the owner's one-time question about install stats on an
// install that has not answered it yet.
type StatsBanner struct {
	PreviewHref string // Settings > Updates, at the card that shows what is sent
}

// statsBanner is the banner for a page view: nil unless the owner is on a GET
// page of a finished setup, the choice is still unset and the server
// configuration does not turn stats off. Editors never reach the service, and
// the Updates page already shows the question as its Install stats card. The
// answer is a POST (see handleStatsChoice), not a browser-side dismissal.
func (a *Admin) statsBanner(r *http.Request, u *CurrentUser, setupPending bool) *StatsBanner {
	if a.deps.Stats == nil || a.deps.Demo.Enabled || u == nil || u.Role != "owner" || setupPending {
		return nil
	}
	if strings.HasPrefix(r.URL.Path, a.adminPath+updatesPath) {
		return nil
	}
	if ask, err := a.deps.Stats.Unanswered(r.Context()); err != nil || !ask {
		return nil
	}
	return &StatsBanner{PreviewHref: a.adminPath + updatesPath + "#updates-stats"}
}
