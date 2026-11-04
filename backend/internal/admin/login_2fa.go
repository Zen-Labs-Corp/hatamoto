package admin

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/twofactor"
)

// pendingCookie carries the signed password-step state to the second step. It
// is not a session: it only grants the right to try a second factor.
const pendingCookie = "hm_2fa"

type twoStepData struct {
	Next string
	// Recovery shows the recovery-code field instead of the app-code one.
	Recovery bool
	// Passkey is true when the user has a passkey to offer.
	Passkey bool
	Status  *Flash
}

// HasStatus ties the field to the status line with aria-describedby.
func (d twoStepData) HasStatus() bool { return d.Status != nil }

func (a *Admin) pendingCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: pendingCookie, Value: value, Path: a.AdminPath(), MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.secure(r)}
}

func (a *Admin) clearPending(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, a.pendingCookie(r, "", -1))
}

// readPending returns the valid pending sign-in of r.
func (a *Admin) readPending(r *http.Request) (twofactor.Pending, bool) {
	c, err := r.Cookie(pendingCookie)
	if err != nil || c.Value == "" {
		return twofactor.Pending{}, false
	}
	p, err := a.deps.PendingKey.Open(c.Value, time.Now())
	return p, err == nil
}

// handleLoginTwoStep is the password step when second factors exist: no
// session is issued for a user who has one, only the pending cookie.
func (a *Admin) handleLoginTwoStep(w http.ResponseWriter, r *http.Request, ps PasswordStep, email, password, next string) {
	ctx := r.Context()
	if len(a.deps.PendingKey) == 0 {
		a.logger().Error("two-step sign-in has no pending key")
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "We could not sign you in. Try again in a moment.")
		return
	}
	ip := middleware.GetClientIP(r)
	u, err := ps.CheckPassword(ctx, email, password)
	if err != nil {
		a.loginFailed(w, r, err, email, next)
		return
	}
	has, err := a.deps.TwoFactor.HasAnyFactor(ctx, u.ID)
	if err != nil {
		// Never a session when we cannot tell.
		a.loginFailed(w, r, err, email, next)
		return
	}
	if !has {
		token, err := ps.IssueVerifiedSession(ctx, u.ID, r.UserAgent(), ip)
		if err != nil {
			a.loginFailed(w, r, err, email, next)
			return
		}
		a.finishSignIn(w, r, token, next)
		return
	}
	p, err := twofactor.NewPending(u.ID, next, time.Now())
	if err != nil {
		a.loginFailed(w, r, err, email, next)
		return
	}
	http.SetCookie(w, a.pendingCookie(r, a.deps.PendingKey.Seal(p), int(twofactor.PendingTTL/time.Second)))
	a.renderTwoStep(w, r, http.StatusOK, p, false, nil)
}

func (a *Admin) renderTwoStep(w http.ResponseWriter, r *http.Request, status int, p twofactor.Pending, recovery bool, flash *Flash) {
	d := twoStepData{Next: p.Next, Recovery: recovery, Status: flash}
	if f, err := a.deps.TwoFactor.Status(r.Context(), p.UserID); err == nil {
		d.Passkey = f.Passkeys > 0
	}
	v := a.NewView(w, r, "Two-step sign-in")
	v.Data = d
	a.Render(w, r, status, "auth/two-step", v)
}

func (a *Admin) handleTwoStepForm(w http.ResponseWriter, r *http.Request) {
	if a.deps.TwoFactor == nil {
		http.Redirect(w, r, a.adminPath+"/login", http.StatusSeeOther)
		return
	}
	p, ok := a.readPending(r)
	if !ok {
		http.Redirect(w, r, a.adminPath+"/login", http.StatusSeeOther)
		return
	}
	a.renderTwoStep(w, r, http.StatusOK, p, r.URL.Query().Get("recovery") == "1", nil)
}

// handleTwoStep verifies the typed code (app code or recovery code) and only
// then issues the session. Every refusal is the same neutral message.
func (a *Admin) handleTwoStep(w http.ResponseWriter, r *http.Request) {
	ps, ok := a.deps.Auth.(PasswordStep)
	if !ok || a.deps.TwoFactor == nil {
		http.Redirect(w, r, a.adminPath+"/login", http.StatusSeeOther)
		return
	}
	p, ok := a.readPending(r)
	if !ok {
		http.Redirect(w, r, a.adminPath+"/login", http.StatusSeeOther)
		return
	}
	recovery := r.PostFormValue("recovery") == "1"
	err := a.deps.TwoFactor.Verify(r.Context(), p.UserID, strings.TrimSpace(r.PostFormValue("code")))
	switch {
	case errors.Is(err, twofactor.ErrWrongCode):
		a.logger().Info("second step refused", "ip", middleware.GetClientIP(r))
		a.renderTwoStep(w, r, http.StatusUnprocessableEntity, p, recovery, &Flash{Kind: "error", Message: loginMessage2FA})
		return
	case err != nil:
		a.logger().Error("second step failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "We could not sign you in. Try again in a moment.")
		return
	}
	a.completeSignIn(w, r, ps, p.UserID, p.Next)
}

// completeSignIn issues the session after a verified second factor.
func (a *Admin) completeSignIn(w http.ResponseWriter, r *http.Request, ps PasswordStep, uid uuid.UUID, next string) {
	token, err := ps.IssueVerifiedSession(r.Context(), uid, r.UserAgent(), middleware.GetClientIP(r))
	if err != nil {
		a.logger().Error("issue session failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "We could not sign you in. Try again in a moment.")
		return
	}
	a.clearPending(w, r)
	a.finishSignIn(w, r, token, SafeNextFor(a.adminPath, next))
}

// loginMessage2FA is the only thing a failed second step says.
const loginMessage2FA = "That code did not work. Check it and try again."
