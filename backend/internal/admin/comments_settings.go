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

// handleCommentSwitch is a stub until its screen lands.
func (a *Admin) handleCommentSwitch(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}

// handleCommentEnablePage is a stub until its screen lands.
func (a *Admin) handleCommentEnablePage(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}

// handleCommentEnable is a stub until its screen lands.
func (a *Admin) handleCommentEnable(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}
