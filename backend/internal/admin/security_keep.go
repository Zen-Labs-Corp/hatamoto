package admin

import (
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/adminpath"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
)

// AddressBanner is the owner notice for a weak admin address nobody accepted.
type AddressBanner struct {
	Path                 string
	ChooseHref, KeepHref string
	// KeepLabel is "Keep <address>…".
	KeepLabel string
}

// securityKeepData is the typed confirm page behind the banner's Keep button.
type securityKeepData struct{ Danger ui.DangerConfirm }

// needsKeep reports whether the address is weak, changeable and not yet
// accepted: the only state in which the banner and Keep apply.
func needsKeep(st AddressState) bool {
	return st.Lock == AddressUnlocked && st.Level == adminpath.LevelWeak && st.AckAt == nil
}

// onSecurityPage is true for the Security pages, where the banner would repeat
// what the page already says.
func (a *Admin) onSecurityPage(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, a.url("settings", "security")+"/")
}

// addressBanner is the banner for a page view: owners only, never charged to
// editors or to pages that are not a plain GET.
func (a *Admin) addressBanner(r *http.Request, u *CurrentUser) *AddressBanner {
	if a.deps.Address == nil || u == nil || u.Role != "owner" || a.onSecurityPage(r) {
		return nil
	}
	st := a.deps.Address.State()
	if !needsKeep(st) {
		return nil
	}
	return &AddressBanner{Path: st.Path, ChooseHref: a.securityChangeURL(), KeepHref: a.securityKeepURL(), KeepLabel: "Keep " + st.Path + "…"}
}

func (a *Admin) securityKeepURL() string { return a.securityAddressURL() + "/keep" }

// mountSecurityKeep mounts Keep. It needs no sudo: it changes nothing but the
// record that an owner accepted the address, and the typed confirm plus CSRF
// is the friction. Demanding the password to dismiss a warning would invert
// what sudo is for.
func (a *Admin) mountSecurityKeep(owner *httpx.Group) {
	owner.HandleFunc("GET /settings/security/admin-address/keep", a.handleAddressKeep)
	owner.HandleFunc("POST /settings/security/admin-address/keep", a.handleAddressKeepSave)
}

func (a *Admin) renderAddressKeep(w http.ResponseWriter, r *http.Request, status int, st AddressState, check bool) {
	dc := a.addressDanger(r, st.Path, a.securityKeepURL(), a.securityAddressURL(), nil)
	if check {
		dc, _ = CheckDangerPhrase(r, dc)
	}
	v := a.NewView(w, r, "Keep admin address")
	v.Section, v.Tab = "security", "admin-address"
	v.Width = "compact"
	v.Crumbs = a.areaCrumbs("admin-address", Crumb{Label: "Keep"})
	v.Data = securityKeepData{Danger: dc}
	a.Render(w, r, status, "settings/security-keep", v)
}

// keepState is the address state when Keep applies. Otherwise it sends the
// owner to the Security page without writing anything and reports false.
func (a *Admin) keepState(w http.ResponseWriter, r *http.Request) (AddressState, bool) {
	if a.deps.Address == nil {
		a.unavailable(w, r, "Admin address")
		return AddressState{}, false
	}
	st := a.deps.Address.State()
	if !needsKeep(st) {
		http.Redirect(w, r, a.securityAddressURL(), http.StatusSeeOther)
		return st, false
	}
	return st, true
}

func (a *Admin) handleAddressKeep(w http.ResponseWriter, r *http.Request) {
	if st, ok := a.keepState(w, r); ok {
		a.renderAddressKeep(w, r, http.StatusOK, st, false)
	}
}

func (a *Admin) handleAddressKeepSave(w http.ResponseWriter, r *http.Request) {
	st, ok := a.keepState(w, r)
	if !ok {
		return
	}
	dc := a.addressDanger(r, st.Path, a.securityKeepURL(), a.securityAddressURL(), nil)
	if _, ok := CheckDangerPhrase(r, dc); !ok {
		a.renderAddressKeep(w, r, http.StatusUnprocessableEntity, st, true)
		return
	}
	if err := a.deps.Address.Keep(r.Context(), userLabel(userFrom(r.Context()))); err != nil {
		// The error text is not logged: it may carry the address.
		a.logger().Error("admin address keep failed")
		msg := "Your choice could not be saved. Nothing changed."
		if why, refused := refusedMessage(err); refused {
			msg = why + " Nothing changed."
		}
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", msg)
		return
	}
	// No address in the detail: the audit sink also writes it to the server log.
	a.audit(r, "admin_address.keep", "admin-address", map[string]any{"level": st.Level.String(), "ip": middleware.GetClientIP(r)})
	a.SetFlash(w, r, "success", "Admin address kept.")
	http.Redirect(w, r, a.securityAddressURL(), http.StatusSeeOther)
}
