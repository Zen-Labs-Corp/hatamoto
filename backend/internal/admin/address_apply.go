package admin

import (
	"errors"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/middleware"
)

// decisionFailed copies a decision that stops the save onto the picker. It
// reports false when the address may go on to Apply.
func decisionFailed(f *addressForm, dec addrDecision) bool {
	switch dec.Outcome {
	case addrNeedPhrase, addrBadPhrase:
		f.Danger = &dec.Danger
	case addrRefused:
		f.Errors = append(f.Errors, ui.FieldError{ID: "custom", Label: "Admin address", Message: dec.Message})
	default:
		return false
	}
	return true
}

// addrOrigin says which page an address change came from.
type addrOrigin int

const (
	addrOriginSecurity addrOrigin = iota // Settings > Security: always a change
	addrOriginWizard                     // the setup wizard, which is only open while setup is unfinished
)

// applyAddress is the one place an address is changed from the admin, for the
// wizard and the Security page alike. When the live address really moves it
// records the change and tells every owner (only the wizard's first choice is just recorded; on Security every change alerts, even if nobody ever chose). Neither the new nor the old
// address goes in the audit detail: the sink also writes it to the server log.
func (a *Admin) applyAddress(r *http.Request, f *addressForm, dec addrDecision, origin addrOrigin) (ApplyResult, error) {
	u := userFrom(r.Context())
	st := a.deps.Address.State()
	before := st.Path
	res, err := a.deps.Address.Apply(r.Context(), ApplyRequest{Path: dec.Path, By: userLabel(u), Ack: dec.Ack})
	if err != nil || res.Path == before {
		return res, err
	}
	mode, level := "custom", ""
	if f.Mode == addrKeep {
		mode = "random"
	}
	if f.Strength != nil {
		level = f.Strength.Label
	}
	ip := middleware.GetClientIP(r)
	if origin == addrOriginWizard && st.SetAt == nil {
		// Setup is unfinished and nobody has chosen yet: this is the first choice over the address the
		// server generated, not a change someone needs warning about.
		a.audit(r, "admin_address.choose", "admin-address", map[string]any{"mode": mode, "level": level, "acknowledged": dec.Ack, "ip": ip})
		return res, nil
	}
	a.audit(r, "admin_address.change", "admin-address", map[string]any{"mode": mode, "level": level, "acknowledged": dec.Ack, "ip": ip})
	a.notifyOwnersAddressChanged(r, res.Path, userLabel(u), ip)
	return res, nil
}

// refusedMessage is the field error for an Apply the service refused, and
// whether err was a refusal at all.
func refusedMessage(err error) (string, bool) {
	if !errors.Is(err, ErrAddressRefused) {
		return "", false
	}
	return sentence(strings.TrimPrefix(err.Error(), ErrAddressRefused.Error()+": ")), true
}

// regenerateCandidate draws a new private candidate into f and stores
// nothing. It returns the status to answer with.
func (a *Admin) regenerateCandidate(r *http.Request, f *addressForm) int {
	status := http.StatusOK
	if next := a.deps.Address.Generate(); next != "" {
		f.Candidate = next
		f = a.finishAddressForm(r, f)
	} else {
		status = http.StatusInternalServerError
		f.Errors = append(f.Errors, ui.FieldError{ID: "addr-candidate", Label: "Private address", Message: "We could not make a new address. The one shown is still good: keep it, or try again."})
	}
	a.evaluateAddress(r.Context(), f)
	return status
}
