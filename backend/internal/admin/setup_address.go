package admin

import (
	"context"
	"html/template"
	"net/http"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/email"
)

// setupAddressPath is where the wizard's Admin address step lives.
const setupAddressPath = "/setup/admin-address"

// addressLockText is the read-only sentence shown instead of the picker when
// the address cannot be changed from the admin.
func addressLockText(st AddressState) string {
	if st.Lock == AddressLockedEnv {
		return "Set by the server environment (" + st.Pinned + ")."
	}
	return "Changing the address is turned off in the demo."
}

// renderStrength draws the strength block once, for the page and for the htmx
// fragment, from the one template file.
func (a *Admin) renderStrength(s *strengthView) template.HTML {
	if s == nil {
		return ""
	}
	_, pages := a.state()
	ps, ok := pages["setup/admin-address-strength"]
	if !ok {
		return ""
	}
	var b strings.Builder
	if err := ps.t.ExecuteTemplate(&b, "main", &View{Data: s}); err != nil {
		a.logger().Error("admin: render address strength", "err", err)
		return ""
	}
	return template.HTML(b.String()) //nolint:gosec // produced by html/template
}

// setupBackToAddress is where Back on the Site step goes; empty when the
// Admin address step is skipped (no address service, or the demo).
func (a *Admin) setupBackToAddress() string {
	if a.deps.Address == nil || a.deps.Address.State().Lock == AddressLockedDemo {
		return ""
	}
	return a.adminPath + setupAddressPath
}

// renderSetupAddress draws the step. f is nil for the read-only view.
func (a *Admin) renderSetupAddress(w http.ResponseWriter, r *http.Request, status int, f *addressForm) {
	st := a.deps.Address.State()
	d := setupData{Steps: steps(1)}
	if st.Lock != AddressUnlocked {
		d.AddrLockText = addressLockText(st)
		d.Address = a.addressOrigin(r) + st.Path
	} else {
		f.StrengthHTML = a.renderStrength(f.Strength)
		d.Addr, d.Errors = f, f.Errors
	}
	v := a.NewView(w, r, "Admin address")
	v.Wide = true
	v.Data = d
	a.Render(w, r, status, "setup/admin-address", v)
}

func (a *Admin) handleSetupAddress(w http.ResponseWriter, r *http.Request) {
	if a.deps.Address == nil || a.deps.Address.State().Lock == AddressLockedDemo {
		http.Redirect(w, r, a.adminPath+"/setup/site", http.StatusSeeOther)
		return
	}
	st := a.deps.Address.State()
	if st.Lock != AddressUnlocked {
		a.renderSetupAddress(w, r, http.StatusOK, nil)
		return
	}
	a.renderSetupAddress(w, r, http.StatusOK, a.newAddressForm(r, st.Path))
}

// handleSetupAddressRegen draws a new private candidate and stores nothing.
func (a *Admin) handleSetupAddressRegen(w http.ResponseWriter, r *http.Request) {
	if a.deps.Address == nil || a.deps.Address.State().Lock != AddressUnlocked {
		http.Redirect(w, r, a.adminPath+setupAddressPath, http.StatusSeeOther)
		return
	}
	f := a.parseAddressForm(r)
	status := a.regenerateCandidate(r, f)
	a.renderSetupAddress(w, r, status, f)
}

// handleSetupAddressStrength is the htmx fragment behind the custom field.
func (a *Admin) handleSetupAddressStrength(w http.ResponseWriter, r *http.Request) {
	if a.deps.Address == nil {
		http.NotFound(w, r)
		return
	}
	custom := r.URL.Query().Get("custom")
	if custom == "" {
		custom = r.URL.Query().Get("address")
	}
	f := a.finishAddressForm(r, &addressForm{Mode: addrCustom, Custom: custom})
	a.evaluateAddress(r.Context(), f)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(a.renderStrength(f.Strength)))
}

func (a *Admin) handleSetupAddressSave(w http.ResponseWriter, r *http.Request) {
	if a.deps.Address == nil || a.deps.Address.State().Lock != AddressUnlocked {
		http.Redirect(w, r, a.adminPath+"/setup/site", http.StatusSeeOther)
		return
	}
	here := a.adminPath + setupAddressPath
	f := a.parseAddressForm(r)
	dec := a.decideAddress(r, f, here, here)
	if decisionFailed(f, dec) {
		a.renderSetupAddress(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	// Continuing with the address already live changes nothing and needs no password.
	if dec.Path == a.deps.Address.State().Path && !dec.Ack {
		http.Redirect(w, r, a.adminPath+"/setup/site", http.StatusSeeOther)
		return
	}
	if s := sessionFrom(r.Context()); s == nil || !a.sudo.store.Active(s.ID) {
		http.Redirect(w, r, a.sudoURL(here, true), http.StatusSeeOther)
		return
	}
	res, err := a.applyAddress(r, f, dec, addrOriginWizard)
	if msg, refused := refusedMessage(err); refused {
		f.Errors = append(f.Errors, ui.FieldError{ID: "custom", Label: "Admin address", Message: msg})
		a.renderSetupAddress(w, r, http.StatusUnprocessableEntity, f)
		return
	} else if err != nil {
		// The error text is not logged: it may carry the address.
		a.logger().Error("setup: apply admin address failed")
		f.Errors = append(f.Errors, ui.FieldError{ID: "custom", Label: "Admin address", Message: "We could not switch to that address. The current one keeps working."})
		a.renderSetupAddress(w, r, http.StatusInternalServerError, f)
		return
	}
	// The old Admin finishes this response; the next page is served at the new address.
	http.Redirect(w, r, res.Path+"/setup/site", http.StatusSeeOther)
}

// setupAddressMail emails the owner their admin address once per process and
// reports whether the mail went out (now or on an earlier visit). It sends
// inline so the Done step only claims what happened.
func (a *Admin) setupAddressMail(ctx context.Context, u *CurrentUser, address string) bool {
	if u == nil || !a.mailConfigured() {
		return false
	}
	if _, done := a.addrMailed.LoadOrStore(u.ID, true); done {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err := a.deps.Mailer.SendAdminAddressSetup(ctx, u.Email, email.AdminAddressSetupData{SiteName: a.siteName(ctx), Address: address})
	if err != nil {
		a.addrMailed.Delete(u.ID)
		a.logger().Warn("admin address mail not sent", "err", err)
		return false
	}
	return true
}
