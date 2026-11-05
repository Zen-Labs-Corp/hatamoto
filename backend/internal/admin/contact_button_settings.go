package admin

import (
	"encoding/json/jsontext"
	"hatamoto.dev/core/internal/contactlink"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/siteconfig"
)

const contactButtonPath = "/settings/contact-button"

func (a *Admin) mountContactButtonSettings(owner *httpx.Group) {
	owner.HandleFunc("GET "+contactButtonPath, a.handleContactButton)
	owner.HandleFunc("POST "+contactButtonPath, a.handleContactButtonSave)
}

type contactButtonData struct {
	Summary   []ui.FieldError
	Errs      map[string]string
	State     siteconfig.ContactButton
	Channels  []ui.Option
	Positions []ui.Option
	// NumberHelp says what a blank number falls back to.
	NumberHelp string
	// Warning is set when the button is on but has nothing to link to.
	Warning string
}

// contactButtonFromForm reads the posted fields; the number and label are trimmed.
func contactButtonFromForm(r *http.Request) siteconfig.ContactButton {
	_ = r.ParseForm() // a malformed body leaves the form empty, which fails validation
	f := r.PostForm
	return siteconfig.ContactButton{
		Enabled:  f.Get("enabled") == "true",
		Channel:  f.Get("channel"),
		Position: f.Get("position"),
		Label:    strings.TrimSpace(f.Get("label")),
		To:       strings.TrimSpace(f.Get("to")),
		Message:  strings.TrimSpace(f.Get("message")),
	}
}

func (a *Admin) handleContactButton(w http.ResponseWriter, r *http.Request) {
	cfg, ok := a.contactButtonConfig(w, r)
	if !ok {
		return
	}
	cb := siteconfig.ParseContactButton(cfg.ThemeExtra)
	if cb.Channel == "" {
		cb.Channel = siteconfig.ContactWhatsApp
	}
	if cb.Position == "" {
		cb.Position = siteconfig.ContactBottomRight
	}
	a.renderContactButton(w, r, http.StatusOK, cfg, cb, nil)
}

func (a *Admin) handleContactButtonSave(w http.ResponseWriter, r *http.Request) {
	cfg, ok := a.contactButtonConfig(w, r)
	if !ok {
		return
	}
	next := contactButtonFromForm(r)
	if verrs := next.Validate(); len(verrs) > 0 {
		errs := make([]ui.FieldError, 0, len(verrs))
		for _, fe := range verrs {
			errs = append(errs, ui.FieldError{ID: contactButtonFieldID(fe.Field), Label: contactButtonFieldLabel(fe.Field), Message: fe.Message})
		}
		a.renderContactButton(w, r, http.StatusUnprocessableEntity, cfg, next, errs)
		return
	}
	raw, err := siteconfig.MergeContactButton(cfg.ThemeExtra, next)
	if err == nil {
		_, err = a.deps.Site.Update(r.Context(), siteconfig.UpdateInput{ThemeExtra: jsontext.Value(raw)})
	}
	if err != nil {
		a.logger().Error("contact button settings save", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The contact button settings could not be saved.")
		return
	}
	// The number itself is not logged, only whether the owner set one.
	a.audit(r, "contact_button.settings", "contact_button", map[string]any{
		"enabled": next.Enabled, "channel": next.Channel, "position": next.Position, "custom_number": next.To != "",
	})
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(r.Context())
	}
	a.SetFlash(w, r, "success", "Contact button settings saved.")
	http.Redirect(w, r, a.adminPath+contactButtonPath, http.StatusSeeOther)
}

// contactButtonConfig loads the site settings; it answers the request itself on failure.
func (a *Admin) contactButtonConfig(w http.ResponseWriter, r *http.Request) (*siteconfig.Config, bool) {
	if a.deps.Site == nil {
		a.unavailable(w, r, "Contact button settings")
		return nil, false
	}
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.logger().Error("contact button settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The contact button settings could not be loaded.")
		return nil, false
	}
	return cfg, true
}

// contactButtonFieldID is the id of the control that carries a validation field.
func contactButtonFieldID(field string) string { return "contact-button-" + field }

func contactButtonFieldLabel(field string) string {
	switch field {
	case "channel":
		return "Opens"
	case "position":
		return "Position"
	case "label":
		return "Button text"
	case "to":
		return "Number or address"
	case "message":
		return "Message"
	}
	return "Contact button"
}

// profileContact is the phone and email of the business profile, the values a
// blank number or address falls back to on a one-locale site.
func profileContact(cfg *siteconfig.Config) (phone, email string) {
	p, err := bizprofile.Parse(cfg.BusinessProfile)
	if err != nil {
		return "", ""
	}
	p = bizprofile.FromLegacy(p, cfg.Phone, cfg.BusinessHours, cfg.PostalAddress)
	return p.Phone, p.Email
}

func (a *Admin) renderContactButton(w http.ResponseWriter, r *http.Request, status int, cfg *siteconfig.Config, cb siteconfig.ContactButton, errs []ui.FieldError) {
	d := contactButtonData{Summary: errs, Errs: map[string]string{}, State: cb}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	d.Channels = []ui.Option{
		{Value: siteconfig.ContactWhatsApp, Label: "WhatsApp", Selected: cb.Channel == siteconfig.ContactWhatsApp},
		{Value: siteconfig.ContactCall, Label: "Phone call", Selected: cb.Channel == siteconfig.ContactCall},
		{Value: siteconfig.ContactSMS, Label: "Text message", Selected: cb.Channel == siteconfig.ContactSMS},
		{Value: siteconfig.ContactEmail, Label: "Email", Selected: cb.Channel == siteconfig.ContactEmail},
	}
	d.Positions = []ui.Option{
		{Value: siteconfig.ContactBottomRight, Label: "Bottom right", Selected: cb.Position != siteconfig.ContactBottomLeft},
		{Value: siteconfig.ContactBottomLeft, Label: "Bottom left", Selected: cb.Position == siteconfig.ContactBottomLeft},
	}
	phone, email := profileContact(cfg)
	target, noun, fallback := phone, "phone number", "No phone number is set in your business details."
	if cb.Channel == siteconfig.ContactEmail {
		target, noun, fallback = email, "email address", "No email address is set in your business details."
	}
	if target != "" {
		d.NumberHelp = "Leave blank to use the " + noun + " from your business details: " + target + "."
	} else {
		d.NumberHelp = "Leave blank to use the " + noun + " from your business details. " + fallback
	}
	if cb.Enabled && cb.To == "" && target == "" {
		d.Warning = fallback + " The button is not shown until you set one here or in your business details."
	}
	if cb.Enabled && cb.Channel == siteconfig.ContactWhatsApp && cb.To == "" && phone != "" {
		if _, intl, _ := contactlink.Phone(phone); !intl {
			d.Warning = "The phone number in your business details has no country code, so WhatsApp cannot open it. The button is not shown until you add one, for example +41 79 123 45 67, here or in your business details."
		}
	}
	view := a.NewView(w, r, "Contact button")
	view.Section, view.Tab = "contact-button", "contact-button"
	view.Width = "narrow"
	view.Data = d
	a.Render(w, r, status, "settings/contactbutton", view)
}
