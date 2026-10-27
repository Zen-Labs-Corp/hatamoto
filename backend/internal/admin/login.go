package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
)

// loginMessage is the only thing a failed sign-in says: bad password, unknown
// address and locked account are indistinguishable.
const loginMessage = "Invalid email or password."

type loginData struct {
	Email, Next string
	// Status is the one message above the fields: a failed sign-in or an expired session.
	Status *Flash
	// DemoLogin is the public demo's sign-in (nil outside demo mode).
	DemoLogin *demoLogin
}

type demoLogin struct{ Email, Password string }

// demoLoginData is the login the demo shows on the sign-in page.
func (a *Admin) demoLoginData() *demoLogin {
	if !a.deps.Demo.ShowLogin() {
		return nil
	}
	return &demoLogin{Email: a.deps.Demo.LoginEmail, Password: a.deps.Demo.LoginPassword}
}

// HasStatus tells the template to tie the inputs to the status line with aria-describedby.
func (d loginData) HasStatus() bool { return d.Status != nil }

func (a *Admin) mountLogin(public, authed *httpx.Group) {
	public.HandleFunc("GET /login", a.handleLoginForm)
	public.HandleFunc("POST /login", a.handleLogin)
	authed.HandleFunc("POST /logout", a.handleLogout)
}

// nextParam reads the post-login target, also accepting the "redirect" name the
// Next login used.
func (a *Admin) nextParam(r *http.Request) string {
	for _, k := range []string{"next", "redirect"} {
		if n := SafeNextFor(a.adminPath, r.FormValue(k)); n != "" {
			return n
		}
	}
	return ""
}

func (a *Admin) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if a.deps.NeedsSetup != nil && a.deps.NeedsSetup(ctx) {
		http.Redirect(w, r, a.adminPath+"/setup", http.StatusSeeOther)
		return
	}
	next := a.nextParam(r)
	if _, _, ok := a.authenticate(r); ok {
		if next == "" {
			next = a.landing()
		}
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	d := loginData{Next: next, DemoLogin: a.demoLoginData()}
	if r.URL.Query().Get("expired") == "1" {
		d.Status = &Flash{Kind: "info", Message: "Your session expired. Sign in again."}
	}
	v := a.NewView(w, r, "Sign in")
	v.Data = d
	a.Render(w, r, http.StatusOK, "auth/login", v)
}

func (a *Admin) handleLogin(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	next := a.nextParam(r)
	if a.deps.Auth == nil {
		a.Error(w, r, http.StatusServiceUnavailable, "Sign-in unavailable", "The server is not ready.")
		return
	}
	_, token, err := a.deps.Auth.LoginSession(r.Context(), email, password, r.UserAgent(), middleware.GetClientIP(r))
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrAccountLocked):
			a.logger().Info("login attempt on locked account", "ip", middleware.GetClientIP(r))
			fallthrough
		case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrUserNotFound), errors.Is(err, auth.ErrUserDisabled):
			v := a.NewView(w, r, "Sign in")
			v.Data = loginData{Email: email, Next: next, DemoLogin: a.demoLoginData(), Status: &Flash{Kind: "error", Message: loginMessage}}
			a.Render(w, r, http.StatusUnprocessableEntity, "auth/login", v)
		case errors.Is(err, context.Canceled):
			// The client left (or was dropped while queued for a hash): nothing to answer.
			a.logger().Debug("sign-in abandoned", "err", err)
		default:
			a.logger().Error("login failed", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "We could not sign you in. Try again in a moment.")
		}
		return
	}
	a.setSession(w, r, token)
	if next == "" {
		next = a.landing()
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// handleLogout revokes this session (every session of the user with all=1) and
// clears both cookies. It is POST-only so a prefetched link cannot sign anyone out.
func (a *Admin) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s := sessionFrom(r.Context()); s != nil && a.deps.Sessions != nil {
		var err error
		if r.PostFormValue("all") == "1" && s.UserID != nil {
			err = a.deps.Sessions.RevokeAllForUser(r.Context(), *s.UserID)
		} else {
			err = a.deps.Sessions.Revoke(r.Context(), s.ID)
		}
		if err != nil {
			a.logger().Error("logout revoke failed", "err", err)
		}
	}
	// Drops cached admin pages and recovery copies. Browsers honour it on HTTPS
	// and localhost; it also clears public-page storage for the origin, by design.
	w.Header().Set("Clear-Site-Data", `"cache", "storage"`)
	a.clearSession(w, r)
	http.Redirect(w, r, a.adminPath+"/login", http.StatusSeeOther)
}
