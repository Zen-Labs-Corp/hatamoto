package admin

import (
	"context"
	"encoding/json/jsontext"
	"net/http"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/consent"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/siteconfig"
)

const privacyPath = "/settings/privacy"

// privacyCountDays is how many days the choice counts cover.
const privacyCountDays = 30

// PrivacyService is the cookie consent settings store and the daily choice
// counts behind Settings > Privacy & tracking > Cookie consent.
type PrivacyService interface {
	Get(ctx context.Context) (consent.Settings, error)
	Save(ctx context.Context, s consent.Settings) error
	Counts(ctx context.Context, days int) ([]consent.DayCounts, error)
}

func (a *Admin) mountPrivacySettings(owner *httpx.Group) {
	owner.HandleFunc("GET "+privacyPath, a.handlePrivacy)
	owner.HandleFunc("POST "+privacyPath, a.handlePrivacySave)
	owner.HandleFunc("POST "+privacyPath+"/email-protection", a.handleEmailProtectionSave)
}

type privacyCategory struct {
	ID, Label string
	Checked   bool
}

type privacyData struct {
	Summary    []ui.FieldError
	Errs       map[string]string
	State      consent.Settings
	Regions    []ui.Option
	Categories []privacyCategory
	Counts     ui.Table
	// EmailShown is false when no site settings store is wired; EmailOn is the
	// stored email protection switch (on by default).
	EmailShown, EmailOn bool
}

// privacyFromForm applies the posted fields over stored. A field the post does
// not carry keeps its stored value; the category list is the checked boxes.
func privacyFromForm(r *http.Request, stored consent.Settings) consent.Settings {
	s := stored
	_ = r.ParseForm() // a malformed body leaves the form empty and keeps the stored values
	f := r.PostForm
	if f.Has("enabled") {
		s.Enabled = f.Get("enabled") == "true"
	}
	if f.Has("region") {
		s.Region = f.Get("region")
	}
	if f.Has("title") {
		s.Title = strings.TrimSpace(f.Get("title"))
	}
	if f.Has("text") {
		s.Text = strings.TrimSpace(f.Get("text"))
	}
	if f.Has("categories_present") {
		s.Categories = nil
		for _, c := range consent.Categories {
			for _, v := range f["categories"] {
				if v == c {
					s.Categories = append(s.Categories, c)
					break
				}
			}
		}
	}
	return s
}

func (a *Admin) handlePrivacy(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Privacy
	if svc == nil {
		a.unavailable(w, r, "Privacy settings")
		return
	}
	set, err := svc.Get(r.Context())
	if err != nil {
		a.logger().Error("privacy settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The privacy settings could not be loaded.")
		return
	}
	a.renderPrivacy(w, r, http.StatusOK, set, nil)
}

func (a *Admin) handlePrivacySave(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Privacy
	if svc == nil {
		a.unavailable(w, r, "Privacy settings")
		return
	}
	stored, err := svc.Get(r.Context())
	if err != nil {
		a.logger().Error("privacy settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The privacy settings could not be loaded.")
		return
	}
	next := privacyFromForm(r, stored)
	if verrs := next.Validate(); len(verrs) > 0 {
		errs := make([]ui.FieldError, 0, len(verrs))
		for _, fe := range verrs {
			errs = append(errs, ui.FieldError{ID: privacyFieldID(fe.Field), Label: privacyFieldLabel(fe.Field), Message: fe.Message})
		}
		a.renderPrivacy(w, r, http.StatusUnprocessableEntity, next, errs)
		return
	}
	if err := svc.Save(r.Context(), next); err != nil {
		a.logger().Error("privacy settings save", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The privacy settings could not be saved.")
		return
	}
	a.audit(r, "privacy.settings", "privacy", map[string]any{
		"enabled": next.Enabled, "region": next.Region, "categories": next.Categories,
	})
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(r.Context())
	}
	a.SetFlash(w, r, "success", "Privacy settings saved.")
	http.Redirect(w, r, a.adminPath+privacyPath, http.StatusSeeOther)
}

// privacyFieldID is the id of the control that carries a validation field.
func privacyFieldID(field string) string { return "privacy-" + field }

func privacyFieldLabel(field string) string {
	switch field {
	case "region":
		return "Show the banner to"
	case "title":
		return "Banner title"
	case "text":
		return "Banner text"
	}
	return "Categories"
}

func (a *Admin) renderPrivacy(w http.ResponseWriter, r *http.Request, status int, set consent.Settings, errs []ui.FieldError) {
	d := privacyData{Summary: errs, Errs: map[string]string{}, State: set}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	d.Regions = []ui.Option{
		{Value: consent.RegionAll, Label: "Every visitor", Selected: set.Region != consent.RegionEU},
		{Value: consent.RegionEU, Label: "Visitors in the EU and UK", Selected: set.Region == consent.RegionEU},
	}
	on := map[string]bool{}
	for _, c := range set.Categories {
		on[c] = true
	}
	for _, c := range consent.Categories {
		d.Categories = append(d.Categories, privacyCategory{
			ID: c, Label: strings.ToUpper(c[:1]) + c[1:], Checked: on[c],
		})
	}
	d.Counts = a.privacyCounts(r)
	if a.deps.Site != nil {
		d.EmailShown, d.EmailOn = true, true
		if cfg, err := a.loadSite(r.Context()); err != nil {
			a.logger().Error("privacy settings: site settings", "err", err)
		} else {
			d.EmailOn = siteconfig.ParseProtectEmail(cfg.ThemeExtra)
		}
	}
	view := a.NewView(w, r, "Privacy")
	view.Section, view.Tab = "privacy-tracking", "privacy"
	view.Width = "narrow"
	view.Data = d
	a.Render(w, r, status, "settings/privacy", view)
}

func (a *Admin) privacyCounts(r *http.Request) ui.Table {
	t := ui.Table{
		ID: "consent-counts", Caption: "Consent choices, last 30 days", BaseURL: a.adminPath + privacyPath,
		Columns: []ui.Col{{Key: "day", Label: "Day"}, {Key: "all", Label: "Accept all"}, {Key: "none", Label: "Reject all"}, {Key: "custom", Label: "Custom"}},
		Empty:   ui.Empty{Icon: "shield", Title: "No choices yet", Body: "Totals appear here once visitors answer the banner."},
	}
	if a.deps.Privacy == nil {
		return t
	}
	days, err := a.deps.Privacy.Counts(r.Context(), privacyCountDays)
	if err != nil {
		a.logger().Error("privacy counts", "err", err)
		t.Error = "The counts could not be loaded."
		return t
	}
	for _, d := range days {
		t.Rows = append(t.Rows, ui.Row{ID: d.Day, Label: d.Day, Cells: []ui.Cell{
			{Text: d.Day}, {Text: strconv.FormatInt(d.All, 10)}, {Text: strconv.FormatInt(d.None, 10)}, {Text: strconv.FormatInt(d.Custom, 10)},
		}})
	}
	return t
}

// handleEmailProtectionSave stores the "Protect email addresses" switch. A
// missing field is off: an unchecked switch posts its off value, and a post
// without the field must not leave protection on by accident.
func (a *Admin) handleEmailProtectionSave(w http.ResponseWriter, r *http.Request) {
	if a.deps.Site == nil {
		a.unavailable(w, r, "Privacy settings")
		return
	}
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.logger().Error("email protection settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The email protection setting could not be loaded.")
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty, which reads as off
	on := r.PostForm.Get("protect_email") == "true"
	raw, err := siteconfig.MergeProtectEmail(cfg.ThemeExtra, on)
	if err == nil {
		_, err = a.deps.Site.Update(r.Context(), siteconfig.UpdateInput{ThemeExtra: jsontext.Value(raw)})
	}
	if err != nil {
		a.logger().Error("email protection settings save", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The email protection setting could not be saved.")
		return
	}
	a.audit(r, "email_protection.settings", "email_protection", map[string]any{"enabled": on})
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(r.Context())
	}
	a.SetFlash(w, r, "success", "Email protection saved.")
	http.Redirect(w, r, a.adminPath+privacyPath, http.StatusSeeOther)
}
