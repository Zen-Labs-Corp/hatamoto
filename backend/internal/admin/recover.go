package admin

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/user"
)

type forgotData struct {
	Configured bool
	Sent       bool
	Email      string
	// Command is the CLI line to run when email is off, filled with the address
	// typed in (GET /admin/forgot?email=), or a placeholder.
	Command string
}

// safeEmail matches addresses that are safe to show inside a shell command: no
// quotes, spaces or shell metacharacters.
var safeEmail = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+$`)

func resetCommand(addr string) string {
	if !safeEmail.MatchString(addr) {
		addr = "you@example.com"
	}
	return "hatamoto user reset-password --email " + addr
}

type resetData struct {
	Token string
	Valid bool
	Error string // password field
	Match string // confirm field
}

func (a *Admin) mountRecovery(public *httpx.Group) {
	public.HandleFunc("GET /forgot", a.handleForgotForm)
	public.HandleFunc("POST /forgot", a.handleForgot)
	public.HandleFunc("GET /reset", a.handleResetForm)
	public.HandleFunc("POST /reset", a.handleReset)
}

func (a *Admin) mailConfigured() bool { return a.deps.Mailer != nil && a.deps.Mailer.IsConfigured() }

// noReferrer keeps one-time tokens out of Referer headers.
func noReferrer(w http.ResponseWriter) { w.Header().Set("Referrer-Policy", "no-referrer") }

func (a *Admin) handleForgotForm(w http.ResponseWriter, r *http.Request) {
	v := a.NewView(w, r, "Reset your password")
	addr := strings.TrimSpace(r.URL.Query().Get("email"))
	v.Data = forgotData{Configured: a.mailConfigured(), Email: addr, Command: resetCommand(addr)}
	a.Render(w, r, http.StatusOK, "auth/forgot", v)
}

// handleForgot always answers the same thing, whether or not the address has an
// account. The lookup and send run in the background so the response time does
// not tell either.
func (a *Admin) handleForgot(w http.ResponseWriter, r *http.Request) {
	addr := strings.TrimSpace(r.PostFormValue("email"))
	d := forgotData{Configured: a.mailConfigured(), Sent: true, Email: addr}
	if d.Configured && addr != "" && a.deps.Finder != nil && a.deps.Tokens != nil {
		if base := a.siteAddress(r); base != "" {
			ctx := context.WithoutCancel(r.Context())
			go a.sendReset(ctx, addr, base, middleware.GetClientIP(r))
		} else {
			// The page answers the same either way (no account oracle); the
			// operator finds the reason here.
			a.logger().Warn("password reset not emailed: no site address configured (set PUBLIC_BASE_URL or Settings > Site)")
		}
	}
	v := a.NewView(w, r, "Check your email")
	v.Data = d
	a.Render(w, r, http.StatusOK, "auth/forgot", v)
}

func (a *Admin) sendReset(ctx context.Context, addr, base, ip string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	u, err := a.deps.Finder.GetByEmail(ctx, addr)
	if err != nil {
		return
	}
	token, err := a.deps.Tokens.IssueReset(ctx, u.ID)
	if err != nil {
		if !errors.Is(err, auth.ErrResetThrottle) {
			a.logger().Error("issue reset token failed", "err", err)
		}
		return
	}
	err = a.deps.Mailer.SendPasswordReset(ctx, u.Email, email.ResetMailData{
		SiteName: a.siteName(ctx), URL: base + a.adminPath + "/reset?token=" + token, ExpiresIn: "1 hour",
	})
	if err != nil {
		a.logger().Error("send reset email failed", "err", err)
	}
	a.logger().Info("password reset requested", "ip", ip)
}

func (a *Admin) siteName(ctx context.Context) string {
	if a.deps.Site != nil {
		if cfg, err := a.deps.Site.Get(ctx); err == nil && cfg != nil && cfg.CompanyName != "" {
			return cfg.CompanyName
		}
	}
	if a.deps.SiteNameFallback != "" {
		return a.deps.SiteNameFallback
	}
	return "Hatamoto"
}

// siteAddress is the configured public origin ("" when the owner has not set
// one). Emailed links are built from it and nothing else: a Host or
// X-Forwarded-Host header is attacker-controlled on /admin/forgot.
func (a *Admin) siteAddress(r *http.Request) string {
	if a.deps.BaseURL == nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(a.deps.BaseURL(r)), "/")
}

// requestOrigin is the origin the signed-in owner is browsing. It is only used
// for a link shown on screen to an authenticated owner (never emailed), so a
// forged header would only point the owner's own copy of the link elsewhere.
func (a *Admin) requestOrigin(r *http.Request) string {
	scheme, host := "http", r.Host
	if isHTTPS(r) {
		scheme = "https"
	}
	if middleware.IsTrustedProxy(r.RemoteAddr) {
		if h := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); h != "" {
			host = h
		}
	}
	return scheme + "://" + host
}

func (a *Admin) handleResetForm(w http.ResponseWriter, r *http.Request) {
	noReferrer(w)
	token := r.URL.Query().Get("token")
	v := a.NewView(w, r, "Choose a new password")
	v.Data = resetData{Token: token, Valid: a.deps.Tokens != nil && a.deps.Tokens.ResetValid(r.Context(), token)}
	a.Render(w, r, http.StatusOK, "auth/reset", v)
}

func (a *Admin) handleReset(w http.ResponseWriter, r *http.Request) {
	noReferrer(w)
	token := r.PostFormValue("token")
	pw, confirm := r.PostFormValue("password"), r.PostFormValue("confirm")
	render := func(status int, d resetData) {
		v := a.NewView(w, r, "Choose a new password")
		v.Data = d
		a.Render(w, r, status, "auth/reset", v)
	}
	switch {
	case len(pw) < auth.MinPasswordLen:
		render(http.StatusUnprocessableEntity, resetData{Token: token, Valid: true, Error: sentence(auth.ErrPasswordShort.Error())})
		return
	case pw != confirm:
		render(http.StatusUnprocessableEntity, resetData{Token: token, Valid: true, Match: "The passwords do not match."})
		return
	}
	if a.deps.Tokens == nil {
		render(http.StatusOK, resetData{Token: token})
		return
	}
	u, err := a.deps.Tokens.ConsumeReset(r.Context(), token, pw)
	if err != nil {
		if errors.Is(err, auth.ErrTokenInvalid) {
			render(http.StatusOK, resetData{Token: token})
			return
		}
		a.logger().Error("reset failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "We could not change the password. Try again.")
		return
	}
	a.signInAfter(w, r, u, "Password updated. You are signed in.")
}

// signInAfter starts a session for u (after reset or invite acceptance) and
// lands on the admin home with a flash message.
func (a *Admin) signInAfter(w http.ResponseWriter, r *http.Request, u *user.User, message string) {
	token, err := a.deps.Auth.IssueSession(r.Context(), u.ID, r.UserAgent(), middleware.GetClientIP(r))
	if err != nil {
		a.logger().Error("issue session failed", "err", err)
		http.Redirect(w, r, a.adminPath+"/login", http.StatusSeeOther)
		return
	}
	a.setSession(w, r, token)
	a.SetFlash(w, r, "success", message)
	http.Redirect(w, r, a.landing(), http.StatusSeeOther)
}
