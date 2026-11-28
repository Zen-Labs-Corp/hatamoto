package admin

import (
	"context"
	"net/http"
	"time"
)

// Install stats levels, as the service and the forms name them.
const (
	statsLevelUnset    = "unset"
	statsLevelOff      = "off"
	statsLevelBasic    = "basic"
	statsLevelDetailed = "detailed"
)

// statsDocsURL is the page that says what each level collects.
const statsDocsURL = "https://hatamoto.dev/docs/operate/install-stats"

// StatsService is the install stats as Settings > Updates, and the setup
// wizard use it. The server implements it on top of the
// installstats package; the admin only validates forms and calls through.
type StatsService interface {
	// View is the stored level and whether the server configuration forces it
	// off.
	View(ctx context.Context) (StatsView, error)
	// ForcedOff reports HATAMOTO_TELEMETRY=off. It reads no state.
	ForcedOff() bool
	// Choose stores the owner's answer: "off" or "detailed".
	Choose(ctx context.Context, level string) error
	// ApplyDefault stores the Detailed default for an install that was never
	// asked, once setup is complete. It is a no-op when the server forbids it.
	ApplyDefault(ctx context.Context) error
}

// StatsView is what the admin shows about install stats.
type StatsView struct {
	Level     string     // "unset", "off", "basic" or "detailed"
	ForcedOff bool       // HATAMOTO_TELEMETRY=off: the owner cannot change it
	LastSent  *time.Time // time of the last successful report
}

// statsTicked reports whether the Settings switch is on: stats are on
// unless the owner turned them off or the server configuration forbids them.
// An unset level counts as on (Detailed is applied by default) and a leftover
// basic level is Detailed.
func statsTicked(v StatsView) bool { return !v.ForcedOff && v.Level != statsLevelOff }

// validStatsLevel is what the switch posts: install stats are Detailed or off.
func validStatsLevel(level string) bool {
	return level == statsLevelOff || level == statsLevelDetailed
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
	if level == statsLevelDetailed {
		a.SetFlash(w, r, "success", "Install stats are on.")
	} else {
		a.SetFlash(w, r, "success", "Install stats are off.")
	}
	http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
}

// handleStatsLevel is the Settings > Updates switch: level=off|detailed.
// Anything else is refused, never read as a default.
func (a *Admin) handleStatsLevel(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	level := r.PostFormValue("level")
	if !validStatsLevel(level) {
		a.Error(w, r, http.StatusBadRequest, "Choose on or off", "Install stats are either on or off.")
		return
	}
	a.statsChoose(w, r, level)
}
