package admin

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/admin/ui"
)

// The spam warnings an owner reads before comments go on. The wording is the
// product copy; tests compare it exactly.
const (
	csetCallout       = "Turning comments on will invite spam. Bots and spammers find comment forms within days. Every comment with a link and every first comment will wait in your moderation queue."
	csetSecondWarning = "Are you sure? Turning comments on will invite spam to this site. You will need to check the moderation queue regularly."
	csetTick          = "I understand this will invite spam"

	// csetNoAddressMsg is the error flash when an owner tries to turn comments on with no site address.
	csetNoAddressMsg = "Set your site address in Settings > General before turning comments on."
)

// commentsNoAddress reports that no site address is configured (PUBLIC_BASE_URL, else the Site
// address setting). Confirmation mails need it; a request's own host is never used.
func (a *Admin) commentsNoAddress(r *http.Request) bool { return a.siteAddress(r) == "" }

// commentSettingsView is the data behind Settings > Comments. Cards are the
// site settings, shown only while comments are on.
type commentSettingsView struct {
	Enabled, Owner bool
	NoAddress      bool
	S              CommentSettings
	Cards          []csetCard
	Types          []cpolType
	FormErrors     []ui.FieldError
}

// csetField is one whole-number setting. Min and Max are the accepted range:
// they match the store's validation, the upper bounds are UI caps only.
type csetField struct {
	Name, Label, Help string
	Min, Max          int
}

// csetNumber is a csetField with the value to show and its error.
type csetNumber struct {
	csetField
	ID, Value, Error string
}

// csetCard is one card of the site settings form.
type csetCard struct {
	ID, Title string
	Numbers   []csetNumber
	Sort      *csetChoice // threads card only
	Captcha   *csetToggle // limits card only
	Notify    *csetChoice // notifications card only
}

// csetChoice is a select (sort order, notify mode); csetToggle the captcha switch.
type csetChoice struct {
	ID, Error string
	Options   []ui.Option
}
type csetToggle struct {
	ID      string
	Checked bool
}

var (
	csetTrustFields = []csetField{
		{"known_min_approved", "Approved comments to become known", "", 1, 1000},
		{"known_min_age_days", "Days since first approval (known)", "", 0, 3650},
		{"trusted_min_approved", "Approved comments to become trusted", "", 1, 10000},
		{"trusted_min_age_days", "Days since first approval (trusted)", "", 0, 3650},
	}
	csetThreadFields = []csetField{
		{"max_depth", "Reply depth", "1 shows a flat list.", 1, 10},
		{"page_size", "Top-level comments per page", "", 1, 200},
		{"auto_close_days", "Close comments after (days)", "0 keeps them open.", 0, 3650},
	}
	csetLimitFields = []csetField{
		{"slow_mode_seconds", "Seconds between comments by one person", "", 0, 86400},
		{"cookie_days", "Remember a confirmed device for (days)", "", 1, 365},
		{"purge_days", "Delete spam and trash after (days)", "", 1, 3650},
	}
)

// csetInts points at the integer behind each numeric field name.
func csetInts(s *CommentSettings) map[string]*int {
	return map[string]*int{
		"known_min_approved": &s.KnownMinApproved, "known_min_age_days": &s.KnownMinAgeDays,
		"trusted_min_approved": &s.TrustedMinApproved, "trusted_min_age_days": &s.TrustedMinAgeDays,
		"max_depth": &s.MaxDepth, "page_size": &s.PageSize, "auto_close_days": &s.AutoCloseDays,
		"slow_mode_seconds": &s.SlowModeSeconds, "cookie_days": &s.CookieDays, "purge_days": &s.PurgeDays,
	}
}

func csetFieldID(name string) string { return "comments-" + strings.ReplaceAll(name, "_", "-") }

// csetValues is the form as the stored settings fill it.
func csetValues(s CommentSettings) url.Values {
	v := url.Values{"sort": {s.Sort}, "notify_mode": {s.NotifyMode}}
	for name, p := range csetInts(&s) {
		v.Set(name, strconv.Itoa(*p))
	}
	if s.Captcha {
		v.Set("captcha", "true")
	}
	return v
}

// csetError is one refused field: the field name and what to tell the owner.
type csetError struct{ Name, Message string }

// csetValidate fills next from the form and checks every range. A refused
// field keeps its stored value in next; the errors say which.
func csetValidate(form url.Values, stored CommentSettings) (next CommentSettings, errs []csetError) {
	next = stored
	ints := csetInts(&next)
	for _, f := range append(append(append([]csetField(nil), csetTrustFields...), csetThreadFields...), csetLimitFields...) {
		n, err := strconv.Atoi(strings.TrimSpace(form.Get(f.Name)))
		if err != nil || n < f.Min || n > f.Max {
			errs = append(errs, csetError{f.Name, "Enter a whole number from " + strconv.Itoa(f.Min) + " to " + strconv.Itoa(f.Max) + "."})
			continue
		}
		*ints[f.Name] = n
	}
	if next.TrustedMinApproved < next.KnownMinApproved && !csetHas(errs, "trusted_min_approved", "known_min_approved") {
		errs = append(errs, csetError{"trusted_min_approved", "Use at least " + strconv.Itoa(next.KnownMinApproved) + ", the number for known commenters."})
	}
	if next.TrustedMinAgeDays < next.KnownMinAgeDays && !csetHas(errs, "trusted_min_age_days", "known_min_age_days") {
		errs = append(errs, csetError{"trusted_min_age_days", "Use at least " + strconv.Itoa(next.KnownMinAgeDays) + ", the number of days for known commenters."})
	}
	switch form.Get("sort") {
	case "newest", "oldest":
		next.Sort = form.Get("sort")
	default:
		errs = append(errs, csetError{"sort", "Choose newest first or oldest first."})
	}
	switch form.Get("notify_mode") {
	case "per_comment", "digest", "off":
		next.NotifyMode = form.Get("notify_mode")
	default:
		errs = append(errs, csetError{"notify_mode", "Choose for each comment, hourly summary or off."})
	}
	next.Captcha = form.Get("captcha") == "true"
	return next, errs
}

// csetHas reports whether any of the named fields already has an error.
func csetHas(errs []csetError, names ...string) bool {
	for _, e := range errs {
		for _, n := range names {
			if e.Name == n {
				return true
			}
		}
	}
	return false
}

// csetChanged names the settings that differ, in form order. Names only: the
// audit detail never holds values.
func csetChanged(old, next CommentSettings) []string {
	var out []string
	oi, ni := csetInts(&old), csetInts(&next)
	for _, f := range append(append(append([]csetField(nil), csetTrustFields...), csetThreadFields...), csetLimitFields...) {
		if *oi[f.Name] != *ni[f.Name] {
			out = append(out, f.Name)
		}
	}
	if old.Sort != next.Sort {
		out = append(out, "sort")
	}
	if old.NotifyMode != next.NotifyMode {
		out = append(out, "notify_mode")
	}
	if old.Captcha != next.Captcha {
		out = append(out, "captcha")
	}
	return out
}

func csetNumbers(fields []csetField, vals url.Values, errs map[string]string) []csetNumber {
	out := make([]csetNumber, 0, len(fields))
	for _, f := range fields {
		out = append(out, csetNumber{csetField: f, ID: csetFieldID(f.Name), Value: vals.Get(f.Name), Error: errs[f.Name]})
	}
	return out
}

func csetCards(vals url.Values, errs map[string]string) []csetCard {
	sort := &csetChoice{ID: csetFieldID("sort"), Error: errs["sort"], Options: []ui.Option{
		{Value: "newest", Label: "Newest first", Selected: vals.Get("sort") != "oldest"},
		{Value: "oldest", Label: "Oldest first", Selected: vals.Get("sort") == "oldest"},
	}}
	mode := vals.Get("notify_mode")
	notify := &csetChoice{ID: csetFieldID("notify_mode"), Error: errs["notify_mode"], Options: []ui.Option{
		{Value: "per_comment", Label: "For each comment", Selected: mode != "digest" && mode != "off"},
		{Value: "digest", Label: "Hourly summary", Selected: mode == "digest"},
		{Value: "off", Label: "Off", Selected: mode == "off"},
	}}
	return []csetCard{
		{ID: "comments-trust", Title: "Trust levels", Numbers: csetNumbers(csetTrustFields, vals, errs)},
		{ID: "comments-threads", Title: "Threads", Numbers: csetNumbers(csetThreadFields, vals, errs), Sort: sort},
		{ID: "comments-limits", Title: "Limits", Numbers: csetNumbers(csetLimitFields, vals, errs), Captcha: &csetToggle{ID: csetFieldID("captcha"), Checked: vals.Get("captcha") == "true"}},
		{ID: "comments-notify", Title: "Notifications", Notify: notify},
	}
}

func (a *Admin) handleCommentSettings(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Comments
	if svc == nil {
		a.unavailable(w, r, "Comments")
		return
	}
	s, err := svc.Settings(r.Context())
	if err != nil {
		a.logger().Error("comment settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The comment settings could not be loaded.")
		return
	}
	a.renderCommentSettings(w, r, http.StatusOK, s, csetValues(s), nil)
}

// renderCommentSettings shows the page with vals in the site settings form
// (the posted values after a refused save) and errs beside their fields.
func (a *Admin) renderCommentSettings(w http.ResponseWriter, r *http.Request, status int, s CommentSettings, vals url.Values, errs []csetError) {
	d := commentSettingsView{Enabled: s.Enabled, Owner: a.can(r.Context(), authz.ActionCommentSettingsManage), NoAddress: a.commentsNoAddress(r), S: s}
	byName := map[string]string{}
	for _, e := range errs {
		byName[e.Name] = e.Message
		label := e.Name
		for _, f := range append(append(append([]csetField(nil), csetTrustFields...), csetThreadFields...), csetLimitFields...) {
			if f.Name == e.Name {
				label = f.Label
			}
		}
		if e.Name == "sort" {
			label = "Sort order"
		}
		if e.Name == "notify_mode" {
			label = "Email moderators"
		}
		d.FormErrors = append(d.FormErrors, ui.FieldError{ID: csetFieldID(e.Name), Label: label, Message: e.Message})
	}
	d.Cards = csetCards(vals, byName)
	if s.Enabled {
		d.Types = a.cpolTypes(r, a.deps.Comments, s.Sort)
	}
	v := a.NewView(w, r, "Comments")
	v.Section, v.Tab = "comments", "comments"
	v.Width = "compact"
	v.Data = d
	a.Render(w, r, status, "settings/comments", v)
}

// handleCommentSettingsSave saves the site settings. Enabled always comes
// from the store: the switch has its own route.
func (a *Admin) handleCommentSettingsSave(w http.ResponseWriter, r *http.Request) {
	if !a.requireCan(w, r, authz.ActionCommentSettingsManage) {
		return
	}
	svc := a.deps.Comments
	if svc == nil {
		a.unavailable(w, r, "Comments")
		return
	}
	ctx := r.Context()
	stored, err := svc.Settings(ctx)
	if err != nil {
		a.logger().Error("comment settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The comment settings could not be loaded.")
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and fails validation
	next, errs := csetValidate(r.PostForm, stored)
	if len(errs) == 0 {
		err = svc.SaveSettings(ctx, next, csetActor(r))
		var fe *CommentFieldError
		switch {
		case errors.As(err, &fe):
			errs = append(errs, csetError{fe.Field, fe.Message})
		case err != nil:
			a.logger().Error("comment settings save", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The comment settings could not be saved.")
			return
		}
	}
	if len(errs) > 0 {
		a.renderCommentSettings(w, r, http.StatusUnprocessableEntity, stored, r.PostForm, errs)
		return
	}
	a.audit(r, "comments.settings", "comments", map[string]any{"changed": csetChanged(stored, next)})
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(ctx)
	}
	a.SetFlash(w, r, "success", "Comment settings saved.")
	Redirect(w, r, a.csetPath())
}

// csetPath is the Settings > Comments page.
func (a *Admin) csetPath() string { return a.adminPath + "/settings/comments" }

// handleCommentSwitch applies the site switch. Turning off is one step. Turning
// on saves nothing: the confirm step (handleCommentEnable) decides.
func (a *Admin) handleCommentSwitch(w http.ResponseWriter, r *http.Request) {
	if !a.requireCan(w, r, authz.ActionCommentSettingsManage) {
		return
	}
	svc := a.deps.Comments
	if svc == nil {
		a.unavailable(w, r, "Comments")
		return
	}
	ctx := r.Context()
	s, err := svc.Settings(ctx)
	if err != nil {
		a.logger().Error("comment settings", "err", err)
		a.SetFlash(w, r, "error", "The comment settings could not be loaded.")
		Redirect(w, r, a.csetPath())
		return
	}
	want := r.PostFormValue("enabled") == "true"
	switch {
	case want && !s.Enabled:
		if a.commentsNoAddress(r) {
			a.SetFlash(w, r, "error", csetNoAddressMsg)
			Redirect(w, r, a.csetPath())
			return
		}
		Redirect(w, r, a.csetPath()+"/enable")
	case !want && s.Enabled:
		was, err := svc.SetEnabled(ctx, false, csetActor(r))
		if err != nil {
			a.logger().Error("comments disable", "err", err)
			a.SetFlash(w, r, "error", "Comments could not be turned off.")
			Redirect(w, r, a.csetPath())
			return
		}
		a.audit(r, "comments.disable", "comments", map[string]any{"old": was, "new": false})
		if a.deps.Invalidate != nil {
			a.deps.Invalidate(ctx)
		}
		a.recountCommentsNav()
		a.SetFlash(w, r, "success", "Comments are off. Nothing new can be posted.")
		Redirect(w, r, a.csetPath())
	default:
		Redirect(w, r, a.csetPath())
	}
}

// handleCommentEnablePage is the no-JS confirm step: the second warning and
// the tick.
func (a *Admin) handleCommentEnablePage(w http.ResponseWriter, r *http.Request) {
	if !a.requireCan(w, r, authz.ActionCommentSettingsManage) {
		return
	}
	if a.deps.Comments == nil {
		a.unavailable(w, r, "Comments")
		return
	}
	a.renderCommentEnable(w, r, http.StatusOK, "")
}

func (a *Admin) renderCommentEnable(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
	v := a.NewView(w, r, "Turn comments on?")
	v.Section, v.Tab = "comments", "comments"
	v.Width = "compact"
	v.Data = struct {
		Err       string
		NoAddress bool
	}{errMsg, a.commentsNoAddress(r)}
	a.Render(w, r, status, "settings/comments-enable", v)
}

// handleCommentEnable turns comments on once the tick is posted. It is the
// only path that calls SetEnabled(true).
func (a *Admin) handleCommentEnable(w http.ResponseWriter, r *http.Request) {
	if !a.requireCan(w, r, authz.ActionCommentSettingsManage) {
		return
	}
	svc := a.deps.Comments
	if svc == nil {
		a.unavailable(w, r, "Comments")
		return
	}
	if a.commentsNoAddress(r) {
		a.SetFlash(w, r, "error", csetNoAddressMsg)
		Redirect(w, r, a.csetPath())
		return
	}
	if r.PostFormValue("understand") != "1" {
		a.renderCommentEnable(w, r, http.StatusUnprocessableEntity, "Tick the box to confirm you understand.")
		return
	}
	ctx := r.Context()
	was, err := svc.SetEnabled(ctx, true, csetActor(r))
	if err != nil {
		a.logger().Error("comments enable", "err", err)
		a.SetFlash(w, r, "error", "Comments could not be turned on.")
		Redirect(w, r, a.csetPath())
		return
	}
	a.audit(r, "comments.enable", "comments", map[string]any{"old": was, "new": true})
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(ctx)
	}
	a.recountCommentsNav()
	a.SetFlash(w, r, "success", "Comments are on. New comments wait in the moderation queue.")
	Redirect(w, r, a.csetPath())
}

// csetActor is the signed-in user's id for the service, "" when there is none.
func csetActor(r *http.Request) string {
	if u := userFrom(r.Context()); u != nil {
		return u.ID
	}
	return ""
}

// recountCommentsNav refreshes the sidebar item after the switch changed.
func (a *Admin) recountCommentsNav() {
	if a.refreshCommentsNav != nil {
		a.refreshCommentsNav()
	}
}
