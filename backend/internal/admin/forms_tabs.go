package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/httpx"
)

// formTabData is the Settings and Share tabs of the form designer. Version is
// the version the page was loaded at; Settings posts it back so a save over
// someone else's change is refused.
type formTabData struct {
	formEdit
	Tabs     []ui.Tab
	Version  string
	Conflict bool
}

func (a *Admin) mountFormTabs(authed *httpx.Group) {
	authed.HandleFunc("GET /forms/design/{id}/{tab}", a.handleFormTab)
	authed.HandleFunc("POST /forms/design/{id}/settings", a.handleFormSettingsSave)
}

func (a *Admin) handleFormTab(w http.ResponseWriter, r *http.Request) {
	tab := r.PathValue("tab")
	if tab != "settings" && tab != "share" {
		a.ErrorBack(w, r, http.StatusNotFound, "Page not found", "That form page does not exist.", a.adminPath+"/forms", "Back to forms")
		return
	}
	f, ok := a.loadForm(w, r)
	if !ok {
		return
	}
	a.renderFormTab(w, r, http.StatusOK, tab, f, formToEdit(f), f.Version(), false)
}

func (a *Admin) renderFormTab(w http.ResponseWriter, r *http.Request, status int, tab string, cur *form.Form, e formEdit, version string, conflict bool) {
	e.Statuses = statusOptions(e.Status)
	if e.Captcha {
		e.CaptchaUnconfigured = a.captchaUnconfigured(r.Context())
		e.SpamLink = userFrom(r.Context()).Role == "owner"
	}
	e.Endpoint = a.formEndpoint(e.Slug)
	e.EmbedCode = fmt.Sprintf("<form action=\"%s\" method=\"POST\">\n  <!-- Add the fields here -->\n  <button type=\"submit\">%s</button>\n</form>", e.Endpoint, orDefault(e.SubmitText, "Submit"))
	for _, k := range []struct{ id, label string }{{"name", "Name"}, {"status", "Status"}, {"notify", "Notification emails"}, {"redirect", "Redirect address"}, {"fields", "Fields"}} {
		if m := e.Errors[k.id]; m != "" {
			e.Summary = append(e.Summary, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	title := "Settings"
	if tab == "share" {
		title = "Share and embed"
	}
	v := a.NewView(w, r, title+": "+cur.Name)
	v.Crumbs = []Crumb{{Label: "Forms", Href: a.adminPath + "/forms"}, {Label: cur.Name, Href: a.adminPath + "/forms/design/" + e.ID}, {Label: title}}
	v.Data = formTabData{formEdit: e, Tabs: a.formDesignTabs(e.ID, tab), Version: version, Conflict: conflict}
	v.Width = "narrow"
	a.Render(w, r, status, "forms/"+tab, v)
}

// handleFormSettingsSave saves the form's details and settings. It never
// reads fields: the stored ones are written back unchanged.
func (a *Admin) handleFormSettingsSave(w http.ResponseWriter, r *http.Request) {
	cur, ok := a.loadForm(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	p := r.PostForm
	t := func(k string) string { return strings.TrimSpace(p.Get(k)) }
	e := formEdit{ID: cur.ID.String(), Slug: cur.Slug, Name: t("name"), Description: t("description"), Status: t("status"),
		SubmitText: t("submit_text"), SuccessMsg: t("success_message"), RedirectURL: t("redirect_url"), NotifyEmail: t("notify_email"), ConfirmMsg: t("confirmation_message"),
		Captcha: t("captcha") != "", Confirmation: t("confirmation") != "", Store: t("store") != "", Errors: map[string]string{}}
	// A page shown again keeps the version it was loaded at.
	base, err := form.ParseVersion(p.Get("version"))
	if err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form version is missing. Reload the page and try again.")
		return
	}
	if e.Name == "" {
		e.Errors["name"] = "Enter a name"
	} else if utf8.RuneCountInString(e.Name) > 120 {
		e.Errors["name"] = "Use at most 120 characters"
	}
	if !validEmailList(e.NotifyEmail) {
		e.Errors["notify"] = "Enter email addresses separated by commas"
	}
	if !form.ValidRedirectTarget(e.RedirectURL) {
		e.Errors["redirect"] = "Enter a path like /thank-you or a full address starting with https://"
	}
	if !form.ValidStatus(e.Status) {
		e.Errors["status"] = "Choose active, inactive or archived"
	}
	if len(e.Errors) > 0 {
		a.renderFormTab(w, r, http.StatusUnprocessableEntity, "settings", cur, e, p.Get("version"), false)
		return
	}
	settings := form.FormSettings{
		SubmitButtonText: orDefault(e.SubmitText, "Submit"), SuccessMessage: orDefault(e.SuccessMsg, "Thank you for your submission!"), SuccessRedirectURL: e.RedirectURL,
		EnableCaptcha: e.Captcha, ConfirmationEmail: e.Confirmation, ConfirmationMessage: e.ConfirmMsg, StoreSubmissions: e.Store,
	}
	if e.NotifyEmail != "" {
		settings.NotifyEmail = &e.NotifyEmail
	}
	desc := e.Description
	_, err = a.deps.Growth.Forms.SaveDefinition(r.Context(), cur.ID, base, &form.SaveDefinitionInput{
		Name: e.Name, Description: &desc, Status: e.Status, Fields: cur.Fields, Settings: settings,
	})
	var defErrs form.DefinitionErrors
	switch {
	case err == nil:
	case errors.Is(err, form.ErrVersionConflict):
		a.renderFormTab(w, r, http.StatusConflict, "settings", cur, e, p.Get("version"), true)
		return
	case errors.Is(err, form.ErrFormNotFound):
		a.ErrorBack(w, r, http.StatusNotFound, "Form not found", "That form does not exist.", a.adminPath+"/forms", "Back to forms")
		return
	case errors.As(err, &defErrs):
		e.Errors["fields"] = "Some fields need attention. Open the Build tab to fix them."
		a.renderFormTab(w, r, http.StatusUnprocessableEntity, "settings", cur, e, p.Get("version"), false)
		return
	default:
		a.logger().Error("save form settings failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The form could not be saved.")
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Form saved.")
	http.Redirect(w, r, a.adminPath+"/forms/design/"+cur.ID.String()+"/settings", http.StatusSeeOther)
}
