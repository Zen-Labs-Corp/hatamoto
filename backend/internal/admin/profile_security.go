package admin

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/twofactor"
)

// recoveryCodeTotal is how many recovery codes a set holds (shown as "7 of 10 left").
const recoveryCodeTotal = twofactor.RecoveryCodeCount

const msgLastFactor = "Your site requires two-step sign-in. Add another method before removing this one."

type securityData struct {
	TOTP          bool
	RecoveryLeft  int
	RecoveryTotal int
	HasFactor     bool
	Required      bool
	Passkeys      ui.Table
	PasskeyCount  int
	// Enrolment page.
	QR     template.HTML
	Key    string
	Status *Flash
	// Codes page.
	Codes     []string
	CodesGone bool
	// RegisterURL is where the passkey page posts its ceremony.
	RegisterURL string
}

// HasStatus ties the code field to the status line.
func (d securityData) HasStatus() bool { return d.Status != nil }

func (a *Admin) mountSecurity(authed *httpx.Group) {
	authed.HandleFunc("GET /profile/security", a.handleSecurity)
	authed.HandleFunc("GET /profile/security/codes", a.handleSecurityCodes)
	// The JSON ceremonies check for a confirmed session themselves: a redirect
	// to the password page would be no answer to a fetch.
	authed.HandleFunc("POST /profile/security/passkeys/begin", a.handlePasskeyAddBegin)
	authed.HandleFunc("POST /profile/security/passkeys/finish", a.handlePasskeyAddFinish)
	sudo := authed.Group("", a.RequireSudo)
	sudo.HandleFunc("POST /profile/security/totp/begin", a.handleTOTPBegin)
	sudo.HandleFunc("GET /profile/security/totp", a.handleTOTPForm)
	sudo.HandleFunc("POST /profile/security/totp", a.handleTOTPConfirm)
	sudo.HandleFunc("POST /profile/security/totp/remove", a.handleTOTPRemove)
	sudo.HandleFunc("POST /profile/security/codes", a.handleCodesRegenerate)
	sudo.HandleFunc("GET /profile/security/passkeys/add", a.handlePasskeyAddPage)
	sudo.HandleFunc("POST /profile/security/passkeys/{id}/remove", a.handlePasskeyRemove)
}

func (a *Admin) securityURL(parts ...string) string {
	return a.url(append([]string{"profile", "security"}, parts...)...)
}

// twoFactorReady answers 404 when two-step sign-in is not wired.
func (a *Admin) twoFactorReady(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	if a.deps.TwoFactor == nil {
		a.Error(w, r, http.StatusNotFound, "Page not found", "That address does not exist in the admin.")
		return uuid.Nil, false
	}
	u := userFrom(r.Context())
	id, err := uuid.Parse(u.ID)
	if err != nil {
		a.Error(w, r, http.StatusForbidden, "Not allowed", "Sign in again.")
		return uuid.Nil, false
	}
	return id, true
}

func (a *Admin) securityFail(w http.ResponseWriter, r *http.Request, what string, err error) {
	a.logger().Error(what, "err", err)
	a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "That did not work. Try again in a moment.")
}

// policyBinds reports whether policy makes a second factor mandatory for role.
func policyBinds(policy, role string) bool { return siteconfig.TwoFactorBinds(policy, role) }

// twoFactorPolicy is the site's sign-in policy ("optional" when nothing is wired).
func (a *Admin) twoFactorPolicy(ctx context.Context) (string, error) {
	if a.deps.Site == nil {
		return siteconfig.TwoFactorOptional, nil
	}
	c, err := a.deps.Site.Get(ctx)
	if err != nil {
		return "", err
	}
	return c.TwoFactorPolicy, nil
}

// requiresFactor reports whether the site policy makes a second factor
// mandatory for role. An unreadable policy counts as required: it only decides
// whether a removal is refused.
func (a *Admin) requiresFactor(ctx context.Context, role string) bool {
	p, err := a.twoFactorPolicy(ctx)
	if err != nil {
		a.logger().Error("read two-factor policy failed", "err", err)
		return true
	}
	return policyBinds(p, role)
}

func (a *Admin) renderSecurity(w http.ResponseWriter, r *http.Request, status int, page, title string, d securityData) {
	v := a.NewView(w, r, title)
	v.Width = "narrow"
	v.Data = d
	a.Render(w, r, status, page, v)
}

func (a *Admin) handleSecurity(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.twoFactorReady(w, r)
	if !ok {
		return
	}
	f, err := a.deps.TwoFactor.Status(r.Context(), uid)
	if err != nil {
		a.securityFail(w, r, "load two-step status failed", err)
		return
	}
	keys, err := a.deps.TwoFactor.Passkeys(r.Context(), uid)
	if err != nil {
		a.securityFail(w, r, "list passkeys failed", err)
		return
	}
	t := ui.Table{
		ID: "passkeys", Caption: "Passkeys", BaseURL: a.securityURL(), CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "name", Label: "Name"}, {Key: "added", Label: "Added"}, {Key: "used", Label: "Last used"}},
		Empty:   ui.Empty{Icon: "key", Title: "No passkeys", Body: "Add one to sign in without a password."},
	}
	for _, k := range keys {
		used := "Never"
		if k.LastUsedAt != nil {
			used = fmtDate(*k.LastUsedAt)
		}
		id := k.ID.String()
		t.Rows = append(t.Rows, ui.Row{ID: id, Label: k.Name, Cells: []ui.Cell{{Text: k.Name}, {Text: fmtDate(k.CreatedAt)}, {Text: used}},
			Actions: []ui.Action{{Label: "Remove", Post: a.securityURL("passkeys", id, "remove"), Danger: true, ConfirmLabel: "Remove passkey",
				Confirm: k.Name + " can no longer be used to sign in."}}})
	}
	a.renderSecurity(w, r, http.StatusOK, "profile/security", "Two-step sign-in", securityData{
		TOTP: f.TOTP, RecoveryLeft: f.RecoveryLeft, RecoveryTotal: recoveryCodeTotal, HasFactor: f.TOTP || f.Passkeys > 0,
		Required: a.requiresFactor(r.Context(), userFrom(r.Context()).Role), Passkeys: t, PasskeyCount: f.Passkeys,
	})
}

// handleTOTPBegin starts an enrolment (the one place a secret is written) and
// shows its page. A GET never writes.
func (a *Admin) handleTOTPBegin(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.twoFactorReady(w, r)
	if !ok {
		return
	}
	account := userFrom(r.Context()).Email
	if _, _, err := a.deps.TwoFactor.PendingSecret(r.Context(), uid, account); errors.Is(err, twofactor.ErrNothingPending) {
		_, _, err = a.deps.TwoFactor.BeginTOTP(r.Context(), uid, account)
		if err != nil && !errors.Is(err, twofactor.ErrAlreadyEnrolled) {
			a.securityFail(w, r, "begin authenticator enrolment failed", err)
			return
		}
	} else if err != nil {
		a.securityFail(w, r, "begin authenticator enrolment failed", err)
		return
	}
	http.Redirect(w, r, a.securityURL("totp"), http.StatusSeeOther)
}

// handleTOTPForm shows the pending enrolment: the secret as a QR code and as
// text, with the form that confirms the first code. It only reads.
func (a *Admin) handleTOTPForm(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.twoFactorReady(w, r)
	if !ok {
		return
	}
	a.renderTOTP(w, r, uid, http.StatusOK, nil)
}

func (a *Admin) renderTOTP(w http.ResponseWriter, r *http.Request, uid uuid.UUID, status int, flash *Flash) {
	key, uri, err := a.deps.TwoFactor.PendingSecret(r.Context(), uid, userFrom(r.Context()).Email)
	if errors.Is(err, twofactor.ErrNothingPending) || errors.Is(err, twofactor.ErrAlreadyEnrolled) {
		http.Redirect(w, r, a.securityURL(), http.StatusSeeOther)
		return
	}
	if err != nil {
		a.securityFail(w, r, "read authenticator enrolment failed", err)
		return
	}
	qr, err := twofactor.QRSVG(uri)
	if err != nil {
		a.securityFail(w, r, "render QR code failed", err)
		return
	}
	a.renderSecurity(w, r, status, "profile/security-totp", "Set up an authenticator app", securityData{QR: qr, Key: groupKey(key), Status: flash})
}

// groupKey spaces the base32 secret in blocks of four so it can be typed.
func groupKey(s string) string {
	var b strings.Builder
	for i, c := range s {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func (a *Admin) handleTOTPConfirm(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.twoFactorReady(w, r)
	if !ok {
		return
	}
	codes, err := a.deps.TwoFactor.ConfirmTOTP(r.Context(), uid, strings.TrimSpace(r.PostFormValue("code")))
	switch {
	case errors.Is(err, twofactor.ErrWrongCode):
		a.renderTOTP(w, r, uid, http.StatusUnprocessableEntity, &Flash{Kind: "error", Message: "That code did not work. Check the app and try again."})
		return
	case errors.Is(err, twofactor.ErrNothingPending):
		http.Redirect(w, r, a.securityURL(), http.StatusSeeOther)
		return
	case err != nil:
		a.securityFail(w, r, "confirm authenticator failed", err)
		return
	}
	a.audit(r, "user.totp.enable", userFrom(r.Context()).Email, nil)
	a.showCodes(w, r, codes)
}

// showCodes parks the plaintext codes in the one-shot store and redirects to
// the page that shows them once.
func (a *Admin) showCodes(w http.ResponseWriter, r *http.Request, codes []string) {
	id := a.secrets.putEntry(secretEntry{user: userFrom(r.Context()).ID, key: strings.Join(codes, "\n"), kind: "recovery"})
	http.Redirect(w, r, a.securityURL("codes")+"?id="+id, http.StatusSeeOther)
}

func (a *Admin) handleSecurityCodes(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.twoFactorReady(w, r); !ok {
		return
	}
	e, ok := a.secrets.take(userFrom(r.Context()).ID, r.URL.Query().Get("id"))
	d := securityData{CodesGone: !ok}
	if ok {
		d.Codes = strings.Split(e.key, "\n")
	}
	w.Header().Set("Cache-Control", "no-store")
	a.renderSecurity(w, r, http.StatusOK, "profile/security-codes", "Your recovery codes", d)
}

func (a *Admin) handleCodesRegenerate(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.twoFactorReady(w, r)
	if !ok {
		return
	}
	has, err := a.deps.TwoFactor.HasAnyFactor(r.Context(), uid)
	if err != nil {
		a.securityFail(w, r, "check second factor failed", err)
		return
	}
	if !has {
		a.SetFlash(w, r, "error", "Set up an authenticator app or a passkey first.")
		http.Redirect(w, r, a.securityURL(), http.StatusSeeOther)
		return
	}
	codes, err := a.deps.TwoFactor.RegenerateCodes(r.Context(), uid)
	if err != nil {
		a.securityFail(w, r, "regenerate recovery codes failed", err)
		return
	}
	a.audit(r, "user.recovery_codes.regenerate", userFrom(r.Context()).Email, nil)
	a.showCodes(w, r, codes)
}

// removalAllowed refuses a removal that would leave a user without any factor
// while the policy requires one. remaining is what would be left.
func (a *Admin) removalAllowed(w http.ResponseWriter, r *http.Request, remaining int) bool {
	if remaining > 0 || !a.requiresFactor(r.Context(), userFrom(r.Context()).Role) {
		return true
	}
	a.SetFlash(w, r, "error", msgLastFactor)
	http.Redirect(w, r, a.securityURL(), http.StatusSeeOther)
	return false
}

func (a *Admin) handleTOTPRemove(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.twoFactorReady(w, r)
	if !ok {
		return
	}
	f, err := a.deps.TwoFactor.Status(r.Context(), uid)
	if err != nil {
		a.securityFail(w, r, "load two-step status failed", err)
		return
	}
	if !f.TOTP {
		http.Redirect(w, r, a.securityURL(), http.StatusSeeOther)
		return
	}
	if !a.removalAllowed(w, r, f.Passkeys) {
		return
	}
	if err := a.deps.TwoFactor.RemoveTOTP(r.Context(), uid); err != nil {
		a.securityFail(w, r, "remove authenticator failed", err)
		return
	}
	a.audit(r, "user.totp.disable", userFrom(r.Context()).Email, nil)
	a.SetFlash(w, r, "success", "Authenticator app removed.")
	http.Redirect(w, r, a.securityURL(), http.StatusSeeOther)
}

func (a *Admin) handlePasskeyRemove(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.twoFactorReady(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		a.Error(w, r, http.StatusNotFound, "Page not found", "That passkey does not exist.")
		return
	}
	f, err := a.deps.TwoFactor.Status(r.Context(), uid)
	if err != nil {
		a.securityFail(w, r, "load two-step status failed", err)
		return
	}
	remaining := f.Passkeys - 1
	if f.TOTP {
		remaining++
	}
	if !a.removalAllowed(w, r, remaining) {
		return
	}
	removed, err := a.deps.TwoFactor.RemovePasskey(r.Context(), uid, id)
	if err != nil {
		a.securityFail(w, r, "remove passkey failed", err)
		return
	}
	if removed {
		a.audit(r, "user.passkey.remove", userFrom(r.Context()).Email, nil)
		a.SetFlash(w, r, "success", "Passkey removed.")
	}
	http.Redirect(w, r, a.securityURL(), http.StatusSeeOther)
}

func (a *Admin) handlePasskeyAddPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.twoFactorReady(w, r); !ok {
		return
	}
	if a.deps.Passkeys == nil {
		a.unavailable(w, r, "Passkeys")
		return
	}
	a.renderSecurity(w, r, http.StatusOK, "profile/security-passkey", "Add a passkey", securityData{RegisterURL: a.securityURL("passkeys")})
}

// passkeyAddReady checks the JSON ceremony preconditions: wired, a confirmed
// session (sudo), a parsable user.
func (a *Admin) passkeyAddReady(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	if a.deps.TwoFactor == nil || a.deps.Passkeys == nil {
		http.NotFound(w, r)
		return uuid.Nil, false
	}
	if s := sessionFrom(r.Context()); s == nil || !a.sudo.store.Active(s.ID) {
		a.writeJSON(w, http.StatusForbidden, map[string]string{"error": "Confirm your password first."})
		return uuid.Nil, false
	}
	id, err := uuid.Parse(userFrom(r.Context()).ID)
	if err != nil {
		a.writeJSON(w, http.StatusForbidden, map[string]string{"error": sudoMessage})
		return uuid.Nil, false
	}
	return id, true
}

func (a *Admin) handlePasskeyAddBegin(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.passkeyAddReady(w, r)
	if !ok {
		return
	}
	u := userFrom(r.Context())
	opts, id, err := a.deps.Passkeys.BeginRegistration(r.Context(), uid, u.Email, displayOr(u))
	if err != nil {
		a.passkeyError(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, passkeyBeginReply{Ceremony: id, Options: opts})
}

func displayOr(u *CurrentUser) string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

func (a *Admin) handlePasskeyAddFinish(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.passkeyAddReady(w, r)
	if !ok {
		return
	}
	name := twofactor.CleanPasskeyName(r.URL.Query().Get("name"))
	if _, err := a.deps.Passkeys.FinishRegistration(r.Context(), r.URL.Query().Get(passkeyCeremonyParam), uid, name, r); err != nil {
		a.passkeyError(w, r, err)
		return
	}
	a.audit(r, "user.passkey.add", userFrom(r.Context()).Email, nil)
	a.SetFlash(w, r, "success", fmt.Sprintf("Passkey %q added.", name))
	a.writeJSON(w, http.StatusOK, map[string]string{"redirect": a.securityURL()})
}
