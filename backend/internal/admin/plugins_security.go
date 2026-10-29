package admin

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
)

// mountPluginSecurity registers the Package security page and the route that
// turns the unsigned-package opt-in off (owner only).
func (a *Admin) mountPluginSecurity(owner *httpx.Group) {
	owner.HandleFunc("GET /plugins/security", a.handlePackageSecurity)
	// Turning it on checks a password: share the sign-in rate limit.
	on := owner
	if a.deps.RateLimit != nil {
		on = owner.Group("", a.deps.RateLimit)
	}
	on.HandleFunc("POST /plugins/unsigned", a.handleAllowUnsigned)
	owner.HandleFunc("POST /plugins/unsigned/off", a.handleStopUnsigned)
}

// pluginSecurityPage is the data of the Package security page.
type pluginSecurityPage struct {
	State     UnsignedState
	UntilText string
	Signers   []SignerInfo
	// SiteHost is the host name the owner has to type to allow unsigned packages.
	SiteHost string
	// NoPassword is set when the signed-in owner has no password to confirm with.
	NoPassword  bool
	ProfileHref string
	Durations   []ui.Option
}

// unsignedUntilText formats the end of a timed opt-in; empty when there is none.
func unsignedUntilText(s UnsignedState) string {
	if !s.On || s.Until == nil {
		return ""
	}
	return s.Until.UTC().Format("2 Jan 2006 15:04 UTC")
}

// packageHost is the host name of this site as the owner sees it: the base URL's
// host, else the request's, without a port. Empty when neither resolves.
func (a *Admin) packageHost(r *http.Request) string {
	if a.deps.BaseURL != nil {
		if u, err := url.Parse(a.deps.BaseURL(r)); err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
	}
	if u, err := url.Parse("//" + r.Host); err == nil {
		return u.Hostname()
	}
	return ""
}

// Durations of an unsigned allowance, as posted by the form.
const (
	unsignedHour    = "1h"
	unsignedDay     = "24h"
	unsignedForever = "forever"
)

func (a *Admin) handlePackageSecurity(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	d := pluginSecurityPage{
		State:       pk.UnsignedState(r.Context()),
		Signers:     pk.Signers(r.Context()),
		SiteHost:    a.packageHost(r),
		ProfileHref: a.url("profile"),
		Durations: []ui.Option{
			{Value: unsignedHour, Label: "1 hour"},
			{Value: unsignedDay, Label: "24 hours"},
			{Value: unsignedForever, Label: "Until I turn it off"},
		},
	}
	d.UntilText = unsignedUntilText(d.State)
	d.NoPassword = !a.hasPassword(r)
	v := a.NewView(w, r, "Package security")
	v.Width = "narrow"
	v.Crumbs = []Crumb{{Label: "Plugins", Href: a.adminPath + "/plugins"}, {Label: "Package security"}}
	v.Data = d
	a.Render(w, r, http.StatusOK, "plugins/security", v)
}

// hasPassword reports whether the signed-in user has a password to confirm
// with. Users without one (or that cannot be read) are blocked, never waved through.
func (a *Admin) hasPassword(r *http.Request) bool {
	s := sessionFrom(r.Context())
	if s == nil || s.UserID == nil {
		return false
	}
	u, err := a.deps.Users.GetByID(r.Context(), *s.UserID)
	return err == nil && u.PasswordHash != ""
}

// unsignedRefused answers a failed gate. Every gate has one generic message and
// the same status, so the page says what to fix and nothing about the password.
func (a *Admin) unsignedRefused(w http.ResponseWriter, r *http.Request, heading, message string) {
	a.ErrorBack(w, r, http.StatusUnprocessableEntity, heading, message, a.adminPath+"/plugins/security", "Back to package security")
}

// handleAllowUnsigned switches the unsigned-package opt-in on. Four gates, all
// checked here whatever the page did: the acknowledgement, the site address
// typed exactly, a duration, and the owner's password. The cheap ones run first
// so a wrong address never costs a password attempt.
func (a *Admin) handleAllowUnsigned(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	if r.PostFormValue("understand") != "1" {
		a.unsignedRefused(w, r, "Tick the box to continue", "Unsigned packages can only be allowed after you confirm that you understand the risk.")
		return
	}
	host := a.packageHost(r)
	if host == "" {
		a.unsignedRefused(w, r, "The site address is unknown", "This server could not work out its own address, so the address check cannot be done.")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(r.PostFormValue("host")), host) {
		a.unsignedRefused(w, r, "Type the site address", "The address you typed does not match this site.")
		return
	}
	var until *time.Time
	duration := r.PostFormValue("duration")
	switch duration {
	case unsignedHour, unsignedDay:
		d, _ := time.ParseDuration(duration)
		t := pluginsNow().UTC().Add(d)
		until = &t
	case unsignedForever:
	default:
		a.unsignedRefused(w, r, "Choose how long", "Pick how long unsigned packages stay allowed.")
		return
	}
	u, s := userFrom(r.Context()), sessionFrom(r.Context())
	if u == nil || s == nil || s.UserID == nil {
		a.toLogin(w, r)
		return
	}
	if !a.hasPassword(r) {
		a.unsignedRefused(w, r, "Set a password first", "Allowing unsigned packages asks for your password, and your account has none yet. Set one on your profile page.")
		return
	}
	pc, ok := a.deps.Auth.(passwordConfirmer)
	if !ok {
		a.Error(w, r, http.StatusServiceUnavailable, "Confirmation unavailable", "The server is not ready.")
		return
	}
	if err := pc.ConfirmPassword(r.Context(), *s.UserID, u.Email, r.PostFormValue("password")); err != nil {
		if r.Context().Err() != nil {
			return
		}
		a.logger().Info("unsigned opt-in password check failed", "err", err)
		a.unsignedRefused(w, r, "Confirmation failed", sudoMessage)
		return
	}
	if err := pk.SetAllowUnsigned(r.Context(), true, until, duration); err != nil {
		a.logger().Error("set allow unsigned failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The setting could not be saved.")
		return
	}
	a.SetFlash(w, r, "success", "Unsigned packages are now accepted. Only install code you trust.")
	http.Redirect(w, r, a.adminPath+"/plugins/security", http.StatusSeeOther)
}

// handleStopUnsigned turns the opt-in off. One click and no other gate: making
// the site safer never needs a password. The Installed page's banner posts
// return=plugins to come back to the list; any other value (or none) lands on
// the security page.
func (a *Admin) handleStopUnsigned(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	if err := pk.SetAllowUnsigned(r.Context(), false, nil, ""); err != nil {
		a.logger().Error("set allow unsigned failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The setting could not be saved.")
		return
	}
	a.SetFlash(w, r, "success", "Unsigned packages are no longer accepted.")
	to := a.adminPath + "/plugins/security"
	if r.PostFormValue("return") == "plugins" {
		to = a.adminPath + "/plugins"
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}
