package admin

import (
	"net/http"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
)

// securityChangeData is the Change address page: the shared picker.
type securityChangeData struct {
	Addr   *addressForm
	Errors []ui.FieldError
	// Action is where the form posts; Regenerate and Strength are its helpers;
	// Back is the Security page.
	Action, Regenerate, Strength, Back string
}

// mountSecurityChange mounts the change flow. The role check runs first so an
// editor gets 403 and never a password prompt; sudo comes second.
func (a *Admin) mountSecurityChange(owner *httpx.Group) {
	sudo := owner.Group("", a.RequireSudo)
	sudo.HandleFunc("GET /settings/security/admin-address/change", a.handleAddressChange)
	sudo.HandleFunc("POST /settings/security/admin-address/change", a.handleAddressChangeSave)
	sudo.HandleFunc("POST /settings/security/admin-address/change/regenerate", a.handleAddressChangeRegen)
	sudo.HandleFunc("GET /settings/security/admin-address/change/strength", a.handleSetupAddressStrength)
}

func (a *Admin) securityAddressURL() string {
	return a.url("settings", "security", "admin-address")
}

func (a *Admin) securityChangeURL() string { return a.securityAddressURL() + "/change" }

// addressChangeAllowed answers 503 when there is no address service and 403
// when the address is pinned or the demo forbids it. It reports whether the
// handler may go on.
func (a *Admin) addressChangeAllowed(w http.ResponseWriter, r *http.Request) bool {
	if a.deps.Address == nil {
		a.unavailable(w, r, "Admin address")
		return false
	}
	if st := a.deps.Address.State(); st.Lock != AddressUnlocked {
		a.Error(w, r, http.StatusForbidden, "Not allowed", addressLockText(st))
		return false
	}
	return true
}

func (a *Admin) renderAddressChange(w http.ResponseWriter, r *http.Request, status int, f *addressForm) {
	f.StrengthHTML = a.renderStrength(f.Strength)
	v := a.NewView(w, r, "Change admin address")
	v.Section, v.Tab = "security", "admin-address"
	v.Width = "compact"
	v.Crumbs = a.areaCrumbs("admin-address", Crumb{Label: "Change"})
	v.Data = securityChangeData{
		Addr: f, Errors: f.Errors, Action: a.securityChangeURL(), Regenerate: a.securityChangeURL() + "/regenerate",
		Strength: a.securityChangeURL() + "/strength", Back: a.securityAddressURL(),
	}
	a.Render(w, r, status, "settings/security-change", v)
}

func (a *Admin) handleAddressChange(w http.ResponseWriter, r *http.Request) {
	if !a.addressChangeAllowed(w, r) {
		return
	}
	candidate := a.deps.Address.Generate()
	f := a.newAddressForm(r, candidate)
	if candidate == "" {
		f.Mode = addrCustom
		f = a.finishAddressForm(r, f)
		f.Errors = append(f.Errors, ui.FieldError{ID: "custom", Label: "Admin address", Message: "We could not make a private address. Choose your own, or try again."})
	}
	a.renderAddressChange(w, r, http.StatusOK, f)
}

// handleAddressChangeRegen draws a new private candidate and stores nothing.
func (a *Admin) handleAddressChangeRegen(w http.ResponseWriter, r *http.Request) {
	if !a.addressChangeAllowed(w, r) {
		return
	}
	f := a.parseAddressForm(r)
	status := a.regenerateCandidate(r, f)
	a.renderAddressChange(w, r, status, f)
}

func (a *Admin) handleAddressChangeSave(w http.ResponseWriter, r *http.Request) {
	if !a.addressChangeAllowed(w, r) {
		return
	}
	f := a.parseAddressForm(r)
	dec := a.decideAddress(r, f, a.securityChangeURL(), a.securityAddressURL())
	if decisionFailed(f, dec) {
		a.renderAddressChange(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	// The address already live, with nothing to acknowledge: write nothing, so
	// who chose it and when stay true.
	if dec.Path == a.deps.Address.State().Path && !dec.Ack {
		a.SetFlash(w, r, "success", "The admin address is unchanged.")
		http.Redirect(w, r, a.securityAddressURL(), http.StatusSeeOther)
		return
	}
	res, err := a.applyAddress(r, f, dec, addrOriginSecurity)
	if msg, refused := refusedMessage(err); refused {
		f.Errors = append(f.Errors, ui.FieldError{ID: "custom", Label: "Admin address", Message: msg})
		a.renderAddressChange(w, r, http.StatusUnprocessableEntity, f)
		return
	} else if err != nil {
		// The error text is not logged: it may carry the address.
		a.logger().Error("admin address change failed")
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The admin address could not be changed. The current one keeps working.")
		return
	}
	a.setFlashAt(w, r, res.Path, "success", "Admin address changed.")
	// The old Admin finishes this response; the next page is served at the new address.
	http.Redirect(w, r, res.Path+"/settings/security/admin-address", http.StatusSeeOther)
}
