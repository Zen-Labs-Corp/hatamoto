package admin

import (
	"fmt"
	"net/http"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/httpx"
)

// formShareData is the Share tab of the form designer.
type formShareData struct {
	Name, Endpoint, EmbedCode string
	Tabs                      []ui.Tab
}

// formSettingsData is the Settings tab: the Build editor bar's data, plus
// what the server renders around the controls the script binds. The values
// themselves come from the boot JSON, so one store and one saver serve both tabs.
type formSettingsData struct {
	formDesignData
	// ReplyTo are the form's email fields; Tokens the fields a subject can name.
	ReplyTo []ui.Option
	Tokens  []string
	// CaptchaUnconfigured: no captcha provider keys are usable. SpamLink is
	// true when the viewer may open the page that fixes it (owners).
	// CaptchaAttrs mark the warning for the script and hide it while the switch is off.
	CaptchaAttrs                  map[string]string
	CaptchaUnconfigured, SpamLink bool
}

func (a *Admin) mountFormTabs(authed *httpx.Group) {
	authed.HandleFunc("GET /forms/design/{id}/{tab}", a.handleFormTab)
	authed.HandleFunc("POST /forms/design/{id}/duplicate", a.handleFormDuplicate)
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
	if tab == "settings" {
		a.renderFormSettings(w, r, f)
		return
	}
	endpoint := a.formEndpoint(f.Slug)
	v := a.NewView(w, r, "Share and embed: "+f.Name)
	v.Crumbs = []Crumb{{Label: "Forms", Href: a.adminPath + "/forms"}, {Label: f.Name, Href: a.adminPath + "/forms/design/" + f.ID.String()}, {Label: "Share and embed"}}
	v.Data = formShareData{
		Name: f.Name, Endpoint: endpoint, Tabs: a.formDesignTabs(r.Context(), f.ID.String(), "share"),
		EmbedCode: fmt.Sprintf("<form action=\"%s\" method=\"POST\">\n  <!-- Add the fields here -->\n  <button type=\"submit\">%s</button>\n</form>", endpoint, orDefault(f.Settings.SubmitButtonText, "Submit")),
	}
	v.Width = "narrow"
	a.Render(w, r, http.StatusOK, "forms/share", v)
}

func (a *Admin) renderFormSettings(w http.ResponseWriter, r *http.Request, f *form.Form) {
	owner := userFrom(r.Context()).Role == "owner"
	boot, _, err := a.formBoot(r.Context(), f, formModeSettings, owner)
	if err != nil {
		a.logger().Error("form settings boot failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The form could not be loaded.")
		return
	}
	id := f.ID.String()
	d := formSettingsData{
		formDesignData: formDesignData{
			ID: id, Name: f.Name, Slug: f.Slug, Status: f.Status,
			Tabs: a.formDesignTabs(r.Context(), id, "settings"), Statuses: formStatusPills(f.Status), Boot: boot,
		},
		ReplyTo:      []ui.Option{{Value: "", Label: "The site address", Selected: f.Settings.NotifyReplyTo == ""}},
		CaptchaAttrs: map[string]string{"data-hm-fs-when": "enable_captcha"}, CaptchaUnconfigured: a.captchaUnconfigured(r.Context()), SpamLink: owner,
	}
	if !f.Settings.EnableCaptcha {
		d.CaptchaAttrs["hidden"] = "hidden"
	}
	for _, fld := range f.Fields {
		if fld.Type == form.FieldTypeEmail {
			d.ReplyTo = append(d.ReplyTo, ui.Option{Value: fld.Name, Label: fld.Label + " (the visitor)", Selected: f.Settings.NotifyReplyTo == fld.Name})
		}
		if fld.IsInput() && fld.Type != form.FieldTypeHidden && fld.Type != form.FieldTypeConsent && len(d.Tokens) < 9 {
			d.Tokens = append(d.Tokens, fld.Name)
		}
	}
	v := a.NewView(w, r, "Settings: "+f.Name)
	v.Crumbs = []Crumb{{Label: "Forms", Href: a.adminPath + "/forms"}, {Label: f.Name, Href: a.adminPath + "/forms/design/" + id}, {Label: "Settings"}}
	v.Data = d
	a.Render(w, r, http.StatusOK, "forms/settings", v)
}
