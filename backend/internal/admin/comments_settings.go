package admin

import (
	"net/http"

	"hatamoto.dev/core/internal/admin/ui"
)

// The spam warnings an owner reads before comments go on. The wording is the
// product copy; tests compare it exactly.
const (
	csetCallout       = "Turning comments on will invite spam. Bots and spammers find comment forms within days. Every comment with a link and every first comment will wait in your moderation queue."
	csetSecondWarning = "Are you sure? Turning comments on will invite spam to this site. You will need to check the moderation queue regularly."
	csetTick          = "I understand this will invite spam"
)

// commentSettingsView is the data behind Settings > Comments.
type commentSettingsView struct {
	Enabled, Owner bool
	S              CommentSettings
	Errors         map[string]string
	FormErrors     []ui.FieldError
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
	a.renderCommentSettings(w, r, http.StatusOK, s, nil)
}

func (a *Admin) renderCommentSettings(w http.ResponseWriter, r *http.Request, status int, s CommentSettings, errs []ui.FieldError) {
	d := commentSettingsView{Enabled: s.Enabled, Owner: csetIsOwner(r), S: s, Errors: map[string]string{}, FormErrors: errs}
	for _, e := range errs {
		d.Errors[e.ID] = e.Message
	}
	v := a.NewView(w, r, "Comments")
	v.Section, v.Tab = "comments", "comments"
	v.Width = "compact"
	v.Data = d
	a.Render(w, r, status, "settings/comments", v)
}

// csetIsOwner reports whether the signed-in user is an owner.
func csetIsOwner(r *http.Request) bool {
	u := userFrom(r.Context())
	return u != nil && u.Role == "owner"
}

// handleCommentSettingsSave is a stub until its screen lands.
func (a *Admin) handleCommentSettingsSave(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}

// csetPath is the Settings > Comments page.
func (a *Admin) csetPath() string { return a.adminPath + "/settings/comments" }

// handleCommentSwitch applies the site switch. Turning off is one step. Turning
// on saves nothing: the confirm step (handleCommentEnable) decides.
func (a *Admin) handleCommentSwitch(w http.ResponseWriter, r *http.Request) {
	if !a.requireOwner(w, r) {
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
		a.SetFlash(w, r, "success", "Comments are off. Nothing new can be posted.")
		Redirect(w, r, a.csetPath())
	default:
		Redirect(w, r, a.csetPath())
	}
}

// handleCommentEnablePage is the no-JS confirm step: the second warning and
// the tick.
func (a *Admin) handleCommentEnablePage(w http.ResponseWriter, r *http.Request) {
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
	v.Data = struct{ Err string }{errMsg}
	a.Render(w, r, status, "settings/comments-enable", v)
}

// handleCommentEnable turns comments on once the tick is posted. It is the
// only path that calls SetEnabled(true).
func (a *Admin) handleCommentEnable(w http.ResponseWriter, r *http.Request) {
	if !a.requireOwner(w, r) {
		return
	}
	svc := a.deps.Comments
	if svc == nil {
		a.unavailable(w, r, "Comments")
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
