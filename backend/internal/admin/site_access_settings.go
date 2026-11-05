package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/siteaccess"
)

const siteAccessPath = "/settings/site-access"

// SiteAccessService is the site access gate as Settings > Site access uses
// it. The server implements it: Save stores the settings and clears the page
// cache, so no visitor is served a page cached under the previous mode.
type SiteAccessService interface {
	Get(ctx context.Context) (siteaccess.Settings, error)
	Save(ctx context.Context, s siteaccess.Settings) error
}

const (
	siteAccessMinPassword = 8
	siteAccessMaxPassword = 128
	siteAccessMaxHeading  = 120
	siteAccessMaxMessage  = 1000
)

// siteAccessData is the Site access page: the saved (or, after a failed save,
// submitted) values. The password and its hash are never part of it.
type siteAccessData struct {
	Summary     []ui.FieldError
	Errs        map[string]string
	Modes       []ui.RadioCardItem
	RetryOpts   []ui.Option
	Heading     string
	Message     string
	HasPassword bool
}

func (a *Admin) mountSiteAccessSettings(owner *httpx.Group) {
	owner.HandleFunc("GET "+siteAccessPath, a.handleSiteAccess)
	owner.HandleFunc("POST "+siteAccessPath, a.handleSiteAccessSave)
}

func (a *Admin) handleSiteAccess(w http.ResponseWriter, r *http.Request) {
	if a.deps.SiteAccess == nil {
		a.unavailable(w, r, "Site access")
		return
	}
	stored, err := a.deps.SiteAccess.Get(r.Context())
	if err != nil {
		a.logger().Error("site access settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The site access settings could not be loaded.")
		return
	}
	a.renderSiteAccess(w, r, http.StatusOK, stored, nil)
}

func (a *Admin) handleSiteAccessSave(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.SiteAccess
	if svc == nil {
		a.unavailable(w, r, "Site access")
		return
	}
	ctx := r.Context()
	stored, err := svc.Get(ctx)
	if err != nil {
		a.logger().Error("site access settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The site access settings could not be loaded.")
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and fails validation
	next, password, errs := validateSiteAccess(r.PostForm, stored)
	if len(errs) > 0 {
		a.renderSiteAccess(w, r, http.StatusUnprocessableEntity, next, errs)
		return
	}
	if password != "" {
		hash, err := auth.HashPasswordContext(ctx, password)
		if err != nil {
			a.logger().Error("site access password hash", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The password could not be saved.")
			return
		}
		next.PasswordHash, next.Epoch = hash, stored.Epoch+1
	}
	if err := svc.Save(ctx, next); err != nil {
		a.logger().Error("site access save", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The site access settings could not be saved.")
		return
	}
	a.audit(r, "site-access.change", "site-access", map[string]any{
		"old_mode": string(stored.Mode), "new_mode": string(next.Mode),
		"retry_after": next.RetryAfter, "password_changed": password != "",
	})
	a.SetFlash(w, r, "success", "Site access saved.")
	http.Redirect(w, r, a.adminPath+siteAccessPath, http.StatusSeeOther)
}

// validateSiteAccess reads the form over the stored settings. The returned
// settings keep the stored hash and epoch; password is the new plain password
// ("" keeps the current one) for the caller to hash. On errors the settings
// carry the submitted values so the page can show them again.
func validateSiteAccess(f map[string][]string, stored siteaccess.Settings) (next siteaccess.Settings, password string, errs []ui.FieldError) {
	get := func(k string) string {
		if v := f[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	next = stored
	fail := func(id, label, msg string) { errs = append(errs, ui.FieldError{ID: id, Label: label, Message: msg}) }

	next.Mode = siteaccess.Mode(get("mode"))
	if !next.Mode.Valid() {
		fail("mode", "Mode", "Choose one of the four modes.")
		next.Mode = stored.Mode
	}
	secs, err := strconv.Atoi(get("retry_after"))
	if err != nil || !siteaccess.ValidRetry(secs) {
		fail("retry-after", "Visitors may retry after", "Choose one of the listed times.")
	} else {
		next.RetryAfter = secs
	}
	next.Heading = strings.TrimSpace(get("heading"))
	if utf8.RuneCountInString(next.Heading) > siteAccessMaxHeading {
		fail("heading", "Heading", "Use "+strconv.Itoa(siteAccessMaxHeading)+" characters or fewer.")
	}
	next.Message = strings.TrimSpace(get("message"))
	if utf8.RuneCountInString(next.Message) > siteAccessMaxMessage {
		fail("message", "Message", "Use "+strconv.Itoa(siteAccessMaxMessage)+" characters or fewer.")
	}
	password = get("password")
	switch n := utf8.RuneCountInString(password); {
	case n == 0 && next.Mode == siteaccess.ModePassword && stored.PasswordHash == "":
		fail("password", "Password", "Set a password to use password mode.")
	case n > 0 && (n < siteAccessMinPassword || n > siteAccessMaxPassword):
		fail("password", "Password", "Use "+strconv.Itoa(siteAccessMinPassword)+" to "+strconv.Itoa(siteAccessMaxPassword)+" characters.")
	}
	return next, password, errs
}

func (a *Admin) renderSiteAccess(w http.ResponseWriter, r *http.Request, status int, s siteaccess.Settings, errs []ui.FieldError) {
	retry := s.RetryAfter
	if !siteaccess.ValidRetry(retry) {
		retry = siteaccess.DefaultRetryAfter
	}
	d := siteAccessData{
		Summary: errs, Errs: map[string]string{},
		Heading: s.Heading, Message: s.Message, HasPassword: s.PasswordHash != "",
		Modes: []ui.RadioCardItem{
			{Value: string(siteaccess.ModeOpen), Title: "Open", Description: "Everyone can see the site.", Checked: s.Mode == siteaccess.ModeOpen},
			{Value: string(siteaccess.ModeComingSoon), Title: "Coming soon", Description: "Visitors see a coming soon page. Search engines are asked not to index it.", Checked: s.Mode == siteaccess.ModeComingSoon},
			{Value: string(siteaccess.ModeMaintenance), Title: "Maintenance", Description: "Visitors see a maintenance page and search engines are told to come back later.", Checked: s.Mode == siteaccess.ModeMaintenance},
			{Value: string(siteaccess.ModePassword), Title: "Password", Description: "Visitors need one shared password to see the site.", Checked: s.Mode == siteaccess.ModePassword},
		},
	}
	for _, o := range siteaccess.RetryOptions {
		d.RetryOpts = append(d.RetryOpts, ui.Option{Value: strconv.Itoa(o), Label: retryLabel(o), Selected: o == retry})
	}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	v := a.NewView(w, r, "Site access")
	v.Section, v.Tab = "access-security", "site-access"
	v.Width = "compact"
	v.Data = d
	a.Render(w, r, status, "settings/site-access", v)
}

func retryLabel(secs int) string {
	switch {
	case secs%86400 == 0:
		return countNoun(secs/86400, "day")
	case secs%3600 == 0:
		return countNoun(secs/3600, "hour")
	}
	return countNoun(secs/60, "minute")
}
