package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
)

// UpdatesService is the automatic-update engine as Settings > Updates and the
// owner banner use it. The server implements it on top of the updater; the
// admin only validates forms and calls through. Every method that changes
// something goes through the same updater path as the scheduled run.
type UpdatesService interface {
	// View is everything the page shows: running version, effective policy,
	// env locks, stored status and history.
	View(ctx context.Context) (UpdatesState, error)
	// Save stores the policy. It returns field errors (keyed by the form
	// element id: mode, channel, window-start, defer) when the updater rejects
	// a value, and ignores locked fields.
	Save(ctx context.Context, in UpdatesForm) (map[string]string, error)
	// Check fetches and decides without downloading anything.
	Check(ctx context.Context) (UpdatesResult, error)
	// Apply starts an update in the background to the target the service
	// derives from its own stored status (ErrNoUpdate when there is none).
	// The caller never names a version.
	Apply(ctx context.Context) (UpdatesResult, error)
	// ApplyWithoutBackup is Apply for an update the pre-update backup
	// stopped: the same run, with the backup skipped for this version only.
	ApplyWithoutBackup(ctx context.Context) (UpdatesResult, error)
	// Unpin clears the stored pin.
	Unpin(ctx context.Context) error
	// Banner is the owner banner, nil when nothing needs attention.
	Banner(ctx context.Context) (*UpdateBanner, error)
	// Glance is the short, cached read behind the top-bar chip and the
	// Overview: it never writes and never costs more than one read per cache
	// window, so every owner page may ask for it.
	Glance(ctx context.Context) (UpdatesGlance, error)
}

// Update decisions the status registry knows (the updater's vocabulary, plus
// the states the page derives itself).
const (
	updUpToDate        = "up_to_date"
	updScheduled       = "scheduled"
	updWaitingWindow   = "waiting_window"
	updWaitingDefer    = "waiting_defer"
	updBlocked         = "blocked"
	updMajor           = "available_major"
	updApplying        = "applying"
	updStaged          = "staged"
	updRolledBack      = "rolled_back"
	updRolledBackYank  = "rolled_back_yanked"
	updNotify          = "notify"
	updRollbackPending = "rollback_pending" // the running release was withdrawn; a check changed nothing
	updBusy            = "busy"
	updFailed          = "update_failed" // Update now stopped with an error
	updBackupFailed    = "backup_failed" // the backup before the update failed, so it was not installed
	updOff             = "off"
	updDev             = "dev"
	updCheckFailed     = "check_failed"
	updUnavailable     = "unavailable"
	updNoCheck         = "no_check"
)

// UpdatesStatus is the outcome of the last update check.
type UpdatesStatus struct {
	LastCheckAt    *time.Time
	LastResult     string // ok | error | skipped | ""
	Decision       string // one of the updater's decisions, "" before the first check
	Reason         string
	Target         string
	AvailableMajor string
}

// UpdatesLocks marks fields the server's environment sets, which the page
// shows read-only and a post cannot change.
type UpdatesLocks struct {
	Mode, Channel       bool
	ModeVar, ChannelVar string
}

// UpdatesHistoryRow is one release switch the supervisor made.
type UpdatesHistoryRow struct {
	When     time.Time
	From, To string
	Phase    string
	Error    string
}

// UpdatesState is the Updates page data. Mode, Channel and the window are the
// effective values (environment over stored over defaults).
type UpdatesState struct {
	Running     string
	Dev         bool // not a release build: updates are disabled
	Mode        string
	Channel     string
	Channels    []string
	WindowStart *int
	WindowEnd   *int
	DeferDays   int
	Pin         string
	Locks       UpdatesLocks
	Status      UpdatesStatus
	Applying    bool // an update started from here is still running
	OffSince    *time.Time
	History     []UpdatesHistoryRow
}

// UpdatesGlance is the small, cached subset of UpdatesState the chip needs.
type UpdatesGlance struct {
	Running  string
	Dev      bool
	Mode     string
	Channel  string
	Status   UpdatesStatus
	Applying bool // an Update now run is in flight; read live, never cached
	OffSince *time.Time
	History  []UpdatesHistoryRow // newest first, at most 20
}

// UpdatesForm is the policy an owner submitted. Locked fields are ignored by
// the service. A nil hour means "any time".
type UpdatesForm struct {
	Mode, Channel          string
	WindowStart, WindowEnd *int
	DeferDays              int
}

// UpdatesResult reports a Check or an Apply.
type UpdatesResult struct {
	Status  UpdatesStatus
	Target  string // Apply: the version being installed
	Started bool   // Apply: a background run was started
	Busy    bool   // another check or update is already running
}

// UpdateBanner is the owner-only notice in the admin shell.
type UpdateBanner struct {
	Kind    string // rolled_back | blocked | backup_failed
	Message string
	Href    string
	Label   string
}

// ErrNoUpdate is Apply's answer when the stored status names nothing to install.
var ErrNoUpdate = errors.New("there is no update to install")

const updatesPath = "/settings/updates"

// offBannerAfter is how long updates must have been off before the chip says so.
const offBannerAfter = 90 * 24 * time.Hour

// ApplyTarget is the version "Update now" installs: a major the owner has to
// start by hand, a scheduled or notified release that skips the ramp, window
// and defer, or the target of an Update now that did not finish (so it can be
// tried again). "" when there is nothing to apply. The service derives the target
// from its stored status with this and never from the form.
func (st UpdatesStatus) ApplyTarget(running string) string {
	t := ""
	switch st.Decision {
	case updMajor:
		t = st.AvailableMajor
	case updScheduled, updWaitingWindow, updWaitingDefer, updNotify, updBusy, updFailed, updBackupFailed:
		t = st.Target
	}
	if t == running {
		return ""
	}
	return t
}

// ApplyTarget is what "Update now" would install on this install: nothing on
// a dev build or while the environment locks updates off (an operator's
// switch-off is not overridden from the page), else the status target.
func (s UpdatesState) ApplyTarget() string {
	if s.Dev || s.Applying || (s.Locks.Mode && s.Mode == "off") {
		return ""
	}
	return s.Status.ApplyTarget(s.Running)
}

// ApplyWithoutBackupTarget is the version "Update without a backup" installs:
// the target of an update the pre-update backup stopped, "" in any other
// state. Like ApplyTarget it comes from the stored status, never a form.
func (s UpdatesState) ApplyWithoutBackupTarget() string {
	if s.Current().Decision != updBackupFailed {
		return ""
	}
	return s.ApplyTarget()
}

// ApplyIsMajor reports that Update now would install a major release, which
// asks for a confirmation first: it can change how data is stored and the
// site cannot go back past it.
func (s UpdatesState) ApplyIsMajor() bool {
	t := s.ApplyTarget()
	return t != "" && majorOf(t) > majorOf(s.Running)
}

// majorOf is the major number of "vMAJOR.MINOR.PATCH"; 0 when unreadable.
// "Major" is decided from the versions, never from the stored decision: a
// retried major is stored as update_failed or busy and still needs the
// confirmation.
func majorOf(v string) int {
	n, _, _ := strings.Cut(strings.TrimPrefix(v, "v"), ".")
	m, _ := strconv.Atoi(n)
	return m
}

// Current is the status the page shows: a dev build and an off mode override
// whatever the last stored check said, and a failed check shows as such.
func (s UpdatesState) Current() UpdatesStatus {
	st := s.Status
	switch {
	case s.Dev:
		st.Decision = updDev
	case s.Mode == "off":
		st.Decision = updOff
	case st.Decision == updFailed, st.Decision == updBackupFailed:
	case st.LastResult == "error":
		st.Decision = updCheckFailed
	case st.Decision == updApplying && st.Target == s.Running:
		// The switch finished: this is the release that was being installed.
		st.Decision, st.Reason, st.Target = updUpToDate, "", ""
	case st.Decision == "" && st.LastCheckAt == nil:
		st.Decision = updNoCheck
	}
	return st
}

// updatesBadges is the status registry: one row per decision, the pill kind
// and its text. Only existing ui.Badge kinds.
var updatesBadges = map[string]struct {
	Kind string
	Text func(UpdatesStatus) string
}{
	updUpToDate: {"success", func(UpdatesStatus) string { return "Up to date" }},
	updScheduled: {"info", func(st UpdatesStatus) string {
		if strings.HasSuffix(st.Reason, readySuffix) {
			return withVersion("Ready to install", st.Target)
		}
		return withVersion("Scheduled in rollout", st.Target)
	}},
	updWaitingWindow: {"info", func(st UpdatesStatus) string { return withVersion("Waiting for the maintenance window", st.Target) }},
	updWaitingDefer:  {"info", func(st UpdatesStatus) string { return withVersion("Deferred", st.Target) }},
	updApplying:      {"info", func(st UpdatesStatus) string { return withVersion("Updating", st.Target) }},
	updStaged:        {"info", func(st UpdatesStatus) string { return withVersion("Staged", st.Target) }},
	updBusy: {"info", func(st UpdatesStatus) string {
		if st.Target == "" {
			return "Checking again later" // a scheduled run that found the box busy: nothing is waiting
		}
		return "Waiting to update"
	}},
	updMajor:           {"info", func(st UpdatesStatus) string { return "Major " + st.AvailableMajor + " available" }},
	updFailed:          {"warning", func(st UpdatesStatus) string { return withVersion("Update failed", st.Target) }},
	updBackupFailed:    {"danger", func(st UpdatesStatus) string { return "Backup failed" }},
	updBlocked:         {"warning", func(UpdatesStatus) string { return "Blocked" }},
	updCheckFailed:     {"warning", func(UpdatesStatus) string { return "Update check failed" }},
	updUnavailable:     {"warning", func(UpdatesStatus) string { return "Updates are unavailable" }},
	updRolledBackYank:  {"warning", func(UpdatesStatus) string { return "Release withdrawn" }},
	updRollbackPending: {"warning", func(UpdatesStatus) string { return "Release withdrawn, rollback pending" }},
	updRolledBack:      {"danger", func(st UpdatesStatus) string { return strings.TrimSpace("Rolled back " + st.Target) }},
	updOff:             {"neutral", func(UpdatesStatus) string { return "Updates are off" }},
	updNotify:          {"neutral", func(st UpdatesStatus) string { return withVersion("Update available, notify only", st.Target) }},
	updDev:             {"neutral", func(UpdatesStatus) string { return "Dev build: updates are disabled" }},
	updNoCheck:         {"neutral", func(UpdatesStatus) string { return "No check yet" }},
}

// updatesStatusBadge is the pill for a status; an empty or unknown decision
// reads as "No check yet" so the page never shows a blank pill.
func updatesStatusBadge(st UpdatesStatus, unavailable bool) ui.Badge {
	d := st.Decision
	if unavailable {
		d = updUnavailable
	}
	row, ok := updatesBadges[d]
	if !ok {
		row = updatesBadges[updNoCheck]
	}
	return ui.Badge{Label: row.Text(st), Kind: row.Kind}
}

// readySuffix ends the reason of a check that found a release due to install
// (the updater's check-only run stores it as scheduled).
const readySuffix = "is ready to install"

func withVersion(label, version string) string {
	if version == "" {
		return label
	}
	return label + ": " + version
}

func withReason(label, reason string) string {
	if reason = strings.TrimSpace(reason); reason == "" {
		return label
	}
	return label + ": " + reason
}

// UpdatesBannerInput is what PickBanner needs: the stored status and the
// effective mode.
type UpdatesBannerInput struct {
	Status  UpdatesStatus
	Mode    string
	Running string
	Dev     bool
}

// bannerReasonRunes keeps a long blocked reason to a line or two; the
// Updates page has all of it.
const bannerReasonRunes = 160

// PickBannerFor returns the single most urgent banner, or nil. Order: rolled
// back, blocked, backup failed: only what needs the owner's action. A major
// release and updates being off for 90+ days are told by the top-bar chip. A
// blocked update is not announced while updates are off (the page says
// "Updates are off"). A dev build never gets one.
func PickBannerFor(prefix string, in UpdatesBannerInput, _ time.Time) *UpdateBanner {
	if in.Dev {
		return nil
	}
	mk := func(kind, msg string) *UpdateBanner {
		return &UpdateBanner{Kind: kind, Message: msg, Href: prefix + updatesPath, Label: "View updates"}
	}
	st := in.Status
	on := in.Mode != "off"
	switch {
	case st.Decision == updRolledBack:
		return mk("rolled_back", rolledBackMessage(st, in.Running))
	case st.Decision == updRolledBackYank:
		return mk("rolled_back", endSentence(withReason("A withdrawn release was rolled back", clip(st.Reason, bannerReasonRunes))))
	case st.Decision == updRollbackPending:
		return mk("rolled_back", "The running release was withdrawn. The next scheduled check rolls the site back.")
	case on && st.Decision == updBlocked:
		return mk("blocked", endSentence(withReason("An update is blocked", clip(st.Reason, bannerReasonRunes))))
	case on && st.Decision == updBackupFailed:
		return mk("backup_failed", endSentence(withReason("The update was not installed because the backup before it failed", clip(st.Reason, bannerReasonRunes))))
	}
	return nil
}

// endSentence ends s with a full stop unless it already ends in punctuation.
func endSentence(s string) string {
	if s == "" || strings.ContainsAny(s[len(s)-1:], ".!?…") {
		return s
	}
	return s + "."
}

// clip cuts s to n runes, adding an ellipsis.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n])) + "…"
	}
	return s
}

func rolledBackMessage(st UpdatesStatus, running string) string {
	back := "the previous version"
	if running != "" {
		back = running
	}
	target := st.Target
	if target == "" {
		target = "the update"
	}
	why := strings.ToLower(st.Reason)
	if why == "" || strings.Contains(why, "health") || strings.Contains(why, "smoke") {
		return fmt.Sprintf("Update rolled back: %s failed its health check and the site went back to %s.", target, back)
	}
	return endSentence(fmt.Sprintf("Update rolled back: %s: %s", target, clip(st.Reason, bannerReasonRunes)))
}

// updatesNow is the clock of the Updates page (tests pin it).
var updatesNow = time.Now

// updatesInput is the policy form as shown: the stored values, or after a
// failed save what the owner submitted.
type updatesInput struct {
	Mode, Channel, Start, End, Defer string
}

// updatesData is the Settings > Updates page.
type updatesData struct {
	Summary []ui.FieldError
	Errs    map[string]string

	Unavailable string // why the updater is not there; "" when it is

	Version, Channel         string
	Status                   ui.Badge
	Stats                    []ui.Stat
	StatusKind               string // callout kind of the status note: info, warning or danger
	HistoryTracker           ui.Tracker
	StatusNote               string
	LastCheck                string
	CanCheck                 bool
	ApplyTarget              string
	NoBackupTarget           string // set in the backup_failed state: the version "Update without a backup" installs
	Applying                 bool
	Zone                     string
	ModeOptions              []ui.Option
	ModeValue, ModeLockNote  string // set when the environment locks the mode: the value, and who set it
	ChannelOptions           []ui.Option
	ChannelValue             string
	ChannelLockNote          string
	StartOptions, EndOptions []ui.Option
	DeferOptions             []ui.Option
	Pin                      string
	History                  ui.Table
	InstallStats             *StatsView // nil when there is no stats service or it failed
	StatsLastSent            string     // time of the last successful report, "" before the first
}

func (a *Admin) mountUpdatesSettings(owner *httpx.Group) {
	owner.HandleFunc("GET /settings/updates", a.handleUpdates)
	owner.HandleFunc("POST /settings/updates", a.handleUpdatesSave)
	owner.HandleFunc("POST /settings/updates/check", a.handleUpdatesCheck)
	owner.HandleFunc("POST /settings/updates/apply", a.handleUpdatesApply)
	owner.HandleFunc("GET /settings/updates/apply-without-backup", a.handleUpdatesApplyWithoutBackup)
	owner.HandleFunc("POST /settings/updates/apply-without-backup", a.handleUpdatesApplyWithoutBackup)
	owner.HandleFunc("POST /settings/updates/unpin", a.handleUpdatesUnpin)
	owner.HandleFunc("POST /settings/updates/stats", a.handleStatsSwitch)
	owner.HandleFunc("POST /settings/updates/stats/choice", a.handleStatsChoice)
	owner.HandleFunc("POST /settings/updates/stats/reset", a.handleStatsReset)
}

// updatesCheckTimeout bounds Check now: it only fetches and decides.
const updatesCheckTimeout = 10 * time.Second

// loadUpdates answers the request itself and returns false when the updater
// is missing or its state cannot be read.
func (a *Admin) loadUpdates(w http.ResponseWriter, r *http.Request) (UpdatesState, bool) {
	if a.deps.Updates == nil {
		a.unavailable(w, r, "Automatic updates")
		return UpdatesState{}, false
	}
	st, err := a.deps.Updates.View(r.Context())
	if err != nil {
		a.logger().Error("updates view", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The update settings could not be loaded.")
		return UpdatesState{}, false
	}
	return st, true
}

func (a *Admin) handleUpdatesSave(w http.ResponseWriter, r *http.Request) {
	st, ok := a.loadUpdates(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and fails validation
	in := updatesInput{
		Mode: st.Mode, Channel: st.Channel,
		Start: strings.TrimSpace(r.PostFormValue("window_start")), End: strings.TrimSpace(r.PostFormValue("window_end")),
		Defer: strings.TrimSpace(r.PostFormValue("defer_days")),
	}
	var ignored []string
	if st.Locks.Mode {
		if v := r.PostFormValue("mode"); v != "" && v != st.Mode {
			ignored = append(ignored, "Mode is set by "+st.Locks.ModeVar)
		}
	} else {
		in.Mode = strings.TrimSpace(r.PostFormValue("mode"))
	}
	if st.Locks.Channel {
		if v := r.PostFormValue("channel"); v != "" && v != st.Channel {
			ignored = append(ignored, "Channel is set by "+st.Locks.ChannelVar)
		}
	} else {
		in.Channel = strings.TrimSpace(r.PostFormValue("channel"))
	}
	form, errs := validateUpdates(in, st)
	if len(errs) == 0 {
		fieldErrs, err := a.deps.Updates.Save(r.Context(), form)
		if err != nil {
			a.logger().Error("updates save", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The update settings could not be saved.")
			return
		}
		errs = updatesFieldErrors(fieldErrs)
	}
	if len(errs) > 0 {
		a.renderUpdates(w, r, http.StatusUnprocessableEntity, &st, &in, errs)
		return
	}
	a.audit(r, "updates.save", "updates", map[string]any{
		"mode": in.Mode, "channel": in.Channel, "window_start": in.Start, "window_end": in.End, "defer_days": form.DeferDays,
	})
	if len(ignored) > 0 {
		a.SetFlash(w, r, "info", "Update settings saved. "+strings.Join(ignored, " and ")+" on the server, so it was not changed.")
	} else {
		a.SetFlash(w, r, "success", "Update settings saved.")
	}
	http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
}

// updatesFieldLabels names the fields in the error summary, by element id.
var updatesFieldLabels = []struct{ ID, Label string }{
	{"mode", "Mode"}, {"channel", "Channel"}, {"window-start", "Maintenance window"}, {"defer", "Defer updates"},
}

func updatesFieldErrors(m map[string]string) []ui.FieldError {
	var out []ui.FieldError
	for _, f := range updatesFieldLabels {
		if msg := m[f.ID]; msg != "" {
			out = append(out, ui.FieldError{ID: f.ID, Label: f.Label, Message: msg})
		}
	}
	return out
}

// validateUpdates turns the submitted strings into a UpdatesForm and the
// errors for what cannot be read. Locked fields were already set to their
// effective values by the caller and are not checked.
func validateUpdates(in updatesInput, st UpdatesState) (UpdatesForm, []ui.FieldError) {
	errs := map[string]string{}
	f := UpdatesForm{Mode: in.Mode, Channel: in.Channel}
	if !st.Locks.Mode && !slices.Contains([]string{"auto", "notify", "off"}, in.Mode) {
		errs["mode"] = "Choose Automatic, Notify only or Off."
	}
	if !st.Locks.Channel && !slices.Contains(st.Channels, in.Channel) {
		errs["channel"] = "Choose one of: " + strings.Join(st.Channels, ", ") + "."
	}
	hour := func(s string) (*int, bool) {
		if s == "" {
			return nil, true
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > 23 {
			return nil, false
		}
		return &n, true
	}
	start, okStart := hour(in.Start)
	end, okEnd := hour(in.End)
	switch {
	case !okStart || !okEnd:
		errs["window-start"] = "Choose hours from 00:00 to 23:00, or Any time."
	case (start == nil) != (end == nil):
		errs["window-start"] = "Set both hours, or leave both on Any time."
	}
	f.WindowStart, f.WindowEnd = start, end
	n, err := strconv.Atoi(in.Defer)
	if err != nil || n < 0 || n > 14 {
		errs["defer"] = "Choose from 0 to 14 days."
	}
	f.DeferDays = n
	return f, updatesFieldErrors(errs)
}

func (a *Admin) handleUpdatesCheck(w http.ResponseWriter, r *http.Request) {
	if a.deps.Updates == nil {
		a.unavailable(w, r, "Automatic updates")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), updatesCheckTimeout)
	defer cancel()
	res, err := a.deps.Updates.Check(ctx)
	a.audit(r, "updates.check", "updates", nil)
	switch {
	case err != nil:
		a.SetFlash(w, r, "warning", "Update check failed: "+fullStop(err.Error()))
	case res.Busy:
		a.SetFlash(w, r, "info", "Another update check is running. Try again in a moment.")
	default:
		kind, msg := updatesCheckFlash(res.Status)
		a.SetFlash(w, r, kind, msg)
	}
	http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
}

// updatesCheckFlash words the result of Check now. A check installs nothing,
// so a release that is due reads as "would update", never "Updating".
func updatesCheckFlash(st UpdatesStatus) (kind, msg string) {
	switch st.Decision {
	case updApplying:
		return "info", fmt.Sprintf("Check finished: would update to %s. Nothing was installed; the next scheduled check installs it, or use Update now.", st.Target)
	case updRolledBackYank, updRollbackPending:
		return "warning", "Check finished: the running release was withdrawn. The next scheduled check would roll the site back. Nothing was changed."
	}
	b := updatesStatusBadge(st, false)
	switch b.Kind {
	case "success":
		kind = "success"
	case "warning", "danger":
		kind = "warning"
	default:
		kind = "info"
	}
	label := b.Label
	if kind == "warning" {
		label = withReason(label, st.Reason)
	}
	return kind, "Check finished: " + label
}

func (a *Admin) handleUpdatesApply(w http.ResponseWriter, r *http.Request) {
	st, ok := a.loadUpdates(w, r)
	if !ok {
		return
	}
	if st.Applying {
		a.SetFlash(w, r, "info", "An update is already running. This page shows the result when it finishes.")
		http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
		return
	}
	// The version never comes from the form: the service derives it, and the
	// button is only shown when there is one, so check before calling it.
	if st.ApplyTarget() == "" {
		a.SetFlash(w, r, "error", "Update not started: "+ErrNoUpdate.Error()+".")
		http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
		return
	}
	if st.ApplyIsMajor() && r.PostFormValue("confirm") != "1" {
		a.renderConfirm(w, r, confirmData{
			Title: "Update to " + st.ApplyTarget() + "?",
			Message: st.ApplyTarget() + " is a major release. It can change how your data is stored, so once it is installed the site cannot go back to " +
				st.Running + " or earlier. A database backup is taken first when it can be. The site restarts briefly.",
			Post: a.adminPath + updatesPath + "/apply", Label: "Update to " + st.ApplyTarget(), Back: a.adminPath + updatesPath, Danger: true,
			Hidden: []hiddenField{{Name: "confirm", Value: "1"}},
		})
		return
	}
	res, err := a.deps.Updates.Apply(r.Context())
	switch {
	case errors.Is(err, ErrNoUpdate):
		a.SetFlash(w, r, "error", "Update not started: "+err.Error()+".")
	case err != nil:
		a.SetFlash(w, r, "error", "Update not started: "+fullStop(err.Error()))
	case res.Busy || !res.Started:
		a.SetFlash(w, r, "info", "An update is already running. This page shows the result when it finishes.")
	default:
		a.audit(r, "updates.apply", res.Target, map[string]any{"target": res.Target})
		a.SetFlash(w, r, "info", "Update started; this page shows the result when it finishes.")
	}
	http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
}

// handleUpdatesApplyWithoutBackup installs the version the pre-update backup
// stopped, with the backup skipped. The version comes from the stored status;
// the form only echoes it back, and a mismatch refuses; a GET, or a POST without the confirmation, only shows the
// confirm page, so nothing runs until the owner confirms.
func (a *Admin) handleUpdatesApplyWithoutBackup(w http.ResponseWriter, r *http.Request) {
	st, ok := a.loadUpdates(w, r)
	if !ok {
		return
	}
	if st.Applying {
		a.SetFlash(w, r, "info", "An update is already running. This page shows the result when it finishes.")
		http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
		return
	}
	target := st.ApplyWithoutBackupTarget()
	if target == "" {
		a.SetFlash(w, r, "error", "Update not started: no update is waiting on a failed backup.")
		http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodPost || r.PostFormValue("confirm") != "1" {
		a.renderConfirm(w, r, confirmData{
			Title: "Update to " + target + " without a backup?",
			Message: "The backup before this update failed. If you continue, " + target + " is installed without one, " +
				"and this cannot be undone from here. This applies to this version only; later updates are backed up again. The site restarts briefly.",
			Post: a.adminPath + updatesPath + "/apply-without-backup", Label: "Update to " + target + " without a backup", Back: a.adminPath + updatesPath, Danger: true,
			Hidden: []hiddenField{{Name: "confirm", Value: "1"}, {Name: "target", Value: target}},
		})
		return
	}
	// The confirmation was for the version the page showed; the status may
	// have moved on since.
	if r.PostFormValue("target") != target {
		a.SetFlash(w, r, "error", "Update not started: the update waiting on a failed backup changed. Review it and try again.")
		http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
		return
	}
	res, err := a.deps.Updates.ApplyWithoutBackup(r.Context())
	switch {
	case errors.Is(err, ErrNoUpdate):
		a.SetFlash(w, r, "error", "Update not started: "+err.Error()+".")
	case err != nil:
		a.SetFlash(w, r, "error", "Update not started: "+fullStop(err.Error()))
	case res.Busy || !res.Started:
		a.SetFlash(w, r, "info", "An update is already running. This page shows the result when it finishes.")
	default:
		a.audit(r, "updates.apply_without_backup", res.Target, map[string]any{"target": res.Target})
		a.SetFlash(w, r, "info", "Update started without a backup; this page shows the result when it finishes.")
	}
	http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
}

func (a *Admin) handleUpdatesUnpin(w http.ResponseWriter, r *http.Request) {
	if a.deps.Updates == nil {
		a.unavailable(w, r, "Automatic updates")
		return
	}
	if err := a.deps.Updates.Unpin(r.Context()); err != nil {
		a.logger().Error("updates unpin", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The pin could not be removed.")
		return
	}
	a.audit(r, "updates.unpin", "updates", nil)
	a.SetFlash(w, r, "success", "Unpinned. Updates follow the channel again.")
	http.Redirect(w, r, a.adminPath+updatesPath, http.StatusSeeOther)
}

func (a *Admin) handleUpdates(w http.ResponseWriter, r *http.Request) {
	if a.deps.Updates == nil {
		a.renderUpdates(w, r, http.StatusOK, nil, nil, nil)
		return
	}
	st, err := a.deps.Updates.View(r.Context())
	if err != nil {
		a.logger().Error("updates view", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The update settings could not be loaded.")
		return
	}
	a.renderUpdates(w, r, http.StatusOK, &st, nil, nil)
}

// renderUpdates draws the page. st is nil when the updater is unavailable; in
// shows the submitted values after a failed save.
func (a *Admin) renderUpdates(w http.ResponseWriter, r *http.Request, status int, st *UpdatesState, in *updatesInput, errs []ui.FieldError) {
	ctx := r.Context()
	loc := a.siteLocation(ctx)
	d := updatesData{Summary: errs, Errs: map[string]string{}, Zone: loc.String()}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	if st == nil {
		d.Unavailable = strings.TrimPrefix(a.deps.UpdatesUnavailable, "update agent: ")
		if d.Unavailable == "" {
			d.Unavailable = "the updater did not start, see the server log"
		}
	} else {
		a.fillUpdates(&d, *st, in, loc)
	}
	if a.deps.Stats != nil && !a.deps.Demo.Enabled {
		if sv, err := a.deps.Stats.View(ctx); err != nil {
			a.logger().Warn("stats view", "err", err)
		} else {
			d.InstallStats = &sv
			if sv.LastSent != nil {
				d.StatsLastSent = fmtDateTime(sv.LastSent.In(loc))
			}
		}
	}
	v := a.NewView(w, r, "Updates")
	v.Section, v.Tab = "updates", "updates"
	v.Width = "compact"
	v.Data = d
	a.Render(w, r, status, "settings/updates", v)
}

func (a *Admin) fillUpdates(d *updatesData, st UpdatesState, in *updatesInput, loc *time.Location) {
	now := updatesNow()
	cur := st.Current()
	d.Version = st.Running
	if st.Dev || d.Version == "" {
		d.Version = "Dev build"
	}
	d.Channel = st.Channel
	d.Status = updatesStatusBadge(cur, false)
	d.StatusNote = updatesNote(st, cur, loc)
	d.LastCheck = lastCheckLine(st.Status, now)
	d.CanCheck = !st.Dev
	d.Applying = st.Applying
	d.ApplyTarget = st.ApplyTarget()
	d.NoBackupTarget = st.ApplyWithoutBackupTarget()
	d.Pin = st.Pin

	if in == nil {
		in = &updatesInput{Mode: st.Mode, Channel: st.Channel, Start: hourText(st.WindowStart), End: hourText(st.WindowEnd), Defer: strconv.Itoa(st.DeferDays)}
	}
	modes := []struct{ Value, Label string }{{"auto", "Automatic"}, {"notify", "Notify only"}, {"off", "Off"}}
	for _, m := range modes {
		d.ModeOptions = append(d.ModeOptions, ui.Option{Value: m.Value, Label: m.Label, Selected: m.Value == in.Mode})
		if st.Locks.Mode && m.Value == st.Mode {
			d.ModeValue, d.ModeLockNote = m.Label, "Set by "+st.Locks.ModeVar+" on the server."
		}
	}
	for _, c := range st.Channels {
		d.ChannelOptions = append(d.ChannelOptions, ui.Option{Value: c, Label: channelLabel(c), Selected: c == in.Channel})
	}
	if st.Locks.Channel {
		d.ChannelValue, d.ChannelLockNote = channelLabel(st.Channel), "Set by "+st.Locks.ChannelVar+" on the server."
	}
	d.StartOptions, d.EndOptions = hourOptions(in.Start), hourOptions(in.End)
	for i := 0; i <= 14; i++ {
		label := countNoun(i, "day")
		d.DeferOptions = append(d.DeferOptions, ui.Option{Value: strconv.Itoa(i), Label: label, Selected: strconv.Itoa(i) == in.Defer})
	}
	d.History = updatesHistoryTableFor(a.adminPath, st.History, loc)
	d.HistoryTracker = updatesHistoryTracker(st.History, loc)
	d.StatusKind = "info"
	if k := d.Status.Kind; k == "warning" || k == "danger" {
		d.StatusKind = k
	}
	d.Stats = []ui.Stat{
		{Label: "Version · " + st.Channel + " channel", Value: d.Version},
		{Label: "Status", Badge: &d.Status},
		{Label: "Last check", Value: d.LastCheck},
	}
}

// updatesHistoryTracker is one block per switch, oldest first, so the row
// reads left to right in time. The phase colours it as the Result badge does,
// except a rollback, which is a warning: the site is safe again.
func updatesHistoryTracker(rows []UpdatesHistoryRow, loc *time.Location) ui.Tracker {
	t := ui.Tracker{Label: "Update history, oldest first"}
	for i := len(rows) - 1; i >= 0; i-- {
		h := rows[i]
		kind := "neutral"
		switch h.Phase {
		case "success":
			kind = "success"
		case "rolled_back":
			kind = "warning"
		case "failed":
			kind = "danger"
		case "staged-offline":
			kind = "info"
		}
		tip := fmtDateTime(h.When.In(loc)) + ": " + h.From + " → " + h.To + ", " + strings.ToLower(updatesHistoryBadge(h).Label)
		t.Blocks = append(t.Blocks, ui.TrackerBlock{Kind: kind, Tip: tip})
	}
	return t
}

func hourText(h *int) string {
	if h == nil {
		return ""
	}
	return strconv.Itoa(*h)
}

// hourOptions is "Any time" plus the 24 hours, value the hour number.
func hourOptions(selected string) []ui.Option {
	opts := []ui.Option{{Value: "", Label: "Any time", Selected: selected == ""}}
	for h := range 24 {
		v := strconv.Itoa(h)
		opts = append(opts, ui.Option{Value: v, Label: fmt.Sprintf("%02d:00", h), Selected: v == selected})
	}
	return opts
}

// updatesNote is the detail line under the status pill, in plain words. It
// carries the reason for the statuses the owner has to act on; the pill stays
// a short label. loc is the site time zone.
func updatesNote(st UpdatesState, cur UpdatesStatus, loc *time.Location) string {
	reason := strings.TrimSpace(cur.Reason)
	switch cur.Decision {
	case updScheduled:
		if strings.HasSuffix(reason, readySuffix) {
			return "It installs at the next scheduled check, or install it now."
		}
		return "Releases reach sites gradually, and this site's turn has not come yet. You can install it now instead."
	case updWaitingWindow:
		if st.WindowStart != nil && st.WindowEnd != nil {
			return fmt.Sprintf("It installs during the maintenance window, %02d:00 to %02d:00 (%s).", *st.WindowStart, *st.WindowEnd, loc)
		}
		return "It installs during the maintenance window."
	case updWaitingDefer:
		if t, ok := deferredUntil(reason); ok {
			return "Held back until " + fmtDateTime(t.In(loc)) + " (" + loc.String() + ")."
		}
		return reason
	case updNotify:
		if i := strings.Index(reason, "; mode notify"); i >= 0 {
			reason = reason[:i]
		}
		return reason + ". You chose Notify only, so it is not installed automatically."
	case updRollbackPending:
		reason, _, _ = strings.Cut(reason, "; check only")
		return reason + ". This check changed nothing; the next scheduled check rolls the site back."
	case updMajor:
		if strings.Contains(reason, "withdrawn") {
			return reason
		}
		return "Major releases are never installed automatically. You are asked to confirm before it installs."
	case updBackupFailed:
		return cur.Target + " was not installed: the backup before the update failed: " + reason
	case updBlocked, updCheckFailed, updRolledBack, updRolledBackYank, updFailed, updBusy, updApplying, updStaged:
		return reason
	}
	return ""
}

// deferredUntil reads the time out of the updater's "deferred until <RFC 3339>".
func deferredUntil(reason string) (time.Time, bool) {
	_, ts, ok := strings.Cut(reason, "deferred until ")
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(ts))
	return t, err == nil
}

func lastCheckLine(st UpdatesStatus, now time.Time) string {
	if st.LastCheckAt == nil {
		return "Never"
	}
	result := "ok"
	switch st.LastResult {
	case "error":
		result = "failed"
	case "skipped":
		result = "skipped"
	}
	return agoText(now.Sub(*st.LastCheckAt)) + " · " + result
}

func agoText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return countNoun(int(d/time.Minute), "minute") + " ago"
	case d < 48*time.Hour:
		return countNoun(int(d/time.Hour), "hour") + " ago"
	}
	return countNoun(int(d/(24*time.Hour)), "day") + " ago"
}

// updatesHistoryBadge is the Result pill of a supervisor event: a short
// label; the error goes under it. A rollback is danger here as in the status.
func updatesHistoryBadge(row UpdatesHistoryRow) ui.Badge {
	switch row.Phase {
	case "success":
		return ui.Badge{Label: "Updated", Kind: "success"}
	case "rolled_back":
		return ui.Badge{Label: "Rolled back", Kind: "danger"}
	case "failed":
		return ui.Badge{Label: "Failed", Kind: "danger"}
	case "manual_rollback", "rolled-back-offline":
		return ui.Badge{Label: "Rolled back by hand", Kind: "neutral"}
	case "staged-offline":
		return ui.Badge{Label: "Staged", Kind: "info"}
	}
	return ui.Badge{Label: row.Phase, Kind: "neutral"}
}

// updatesHistoryTable has two columns so the result is on screen at 390 px:
// the update (when, and from which version to which) and its result, which
// takes the slack and wraps the error under the badge.
func updatesHistoryTableFor(prefix string, rows []UpdatesHistoryRow, loc *time.Location) ui.Table {
	t := ui.Table{
		ID: "update-history", Caption: "Update history", BaseURL: prefix + updatesPath,
		Columns: []ui.Col{{Key: "update", Label: "Update"}, {Key: "result", Label: "Result", Role: "title"}},
		Empty:   ui.Empty{Icon: "refresh", Title: "No updates yet."},
	}
	for i, h := range rows {
		badge := updatesHistoryBadge(h)
		t.Rows = append(t.Rows, ui.Row{ID: strconv.Itoa(i + 1), Label: h.To, Cells: []ui.Cell{
			{Text: fmtDateTime(h.When.In(loc)), Nowrap: true, Sub: h.From + " → " + h.To},
			{Badge: &badge, Sub: strings.TrimSpace(h.Error)},
		}})
	}
	return t
}

// updateBanner is the owner banner for a page view. Editors never reach the
// service; a service error or a missing service means no banner.
func (a *Admin) updateBanner(r *http.Request, u *CurrentUser) *UpdateBanner {
	if a.deps.Updates == nil || u == nil || u.Role != "owner" || r.URL.Path == a.adminPath+updatesPath {
		return nil
	}
	b, err := a.deps.Updates.Banner(r.Context())
	if err != nil || b == nil {
		return nil
	}
	// The service caches and shares b, so link a copy: the admin owns the address.
	out := *b
	out.Href = a.adminPath + updatesPath
	return &out
}

// CalloutKind is the hm/callout kind that styles the banner.
func (b *UpdateBanner) CalloutKind() string {
	switch b.Kind {
	case "rolled_back", "backup_failed":
		return "danger"
	}
	return "warning"
}

// DismissKey is the session-scoped dismissal key; a danger banner (a rollback or a failed
// backup) is never dismissible because it is an error the owner has to act on.
func (b *UpdateBanner) DismissKey() string {
	if b.CalloutKind() == "danger" {
		return ""
	}
	return "update-" + b.Kind
}

// SetUpdates sets the updates service (or why there is none) after New. The
// server mounts the admin before the update agent exists; call it before
// the server starts serving.
func (a *Admin) SetUpdates(svc UpdatesService, unavailable string) {
	a.deps.Updates, a.deps.UpdatesUnavailable = svc, unavailable
}

// channelLabel is the admin name of an update channel. Canary is bleeding edge:
// it gets each release before anyone else has run it.
func channelLabel(c string) string {
	switch c {
	case "stable":
		return "Stable (recommended for live sites)"
	case "edge":
		return "Edge (2 days early)"
	case "canary":
		return "Canary (bleeding edge, may break)"
	}
	return c
}
