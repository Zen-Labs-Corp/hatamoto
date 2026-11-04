package admin

import (
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// twoFactorGateExempt are the paths (below the admin prefix) a user who still
// owes a second factor can reach: the profile pages that set one up, sign out,
// static assets and the password confirmation those pages ask for.
var twoFactorGateExempt = []string{"/profile", "/logout", "/_", "/sudo"}

// twoFactorGateExemptExact are exempt as themselves only (the colour-scheme switch).
var twoFactorGateExemptExact = []string{"/theme"}

func twoFactorGateExemptedIn(prefix, full string) bool {
	path, ok := strings.CutPrefix(full, prefix)
	if !ok {
		return false
	}
	if slices.Contains(twoFactorGateExemptExact, path) {
		return true
	}
	for _, p := range twoFactorGateExempt {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// RequireTwoFactor limits a user the site policy binds but who has no second
// factor to the Security page and sign-out. It runs after RequireSession and
// before RequireProfile. It is defence in depth, not the sign-in check, so a
// failed lookup lets the request through (sign-in itself fails closed). A GET
// is sent to the Security page, an htmx request gets HX-Redirect and anything
// else is redirected too: replaying a POST is not safe.
func (a *Admin) RequireTwoFactor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.deps.TwoFactor == nil || twoFactorGateExemptedIn(a.adminPath, r.URL.Path) || a.inSetupWizard(r) {
			next.ServeHTTP(w, r)
			return
		}
		u := userFrom(r.Context())
		if u == nil {
			next.ServeHTTP(w, r)
			return
		}
		policy, err := a.twoFactorPolicy(r.Context())
		if err != nil {
			a.logger().Error("two-factor gate policy lookup failed", "err", err)
			next.ServeHTTP(w, r)
			return
		}
		if !policyBinds(policy, u.Role) {
			next.ServeHTTP(w, r)
			return
		}
		id, err := uuid.Parse(u.ID)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		has, err := a.deps.TwoFactor.HasAnyFactor(r.Context(), id)
		switch {
		case err != nil:
			a.logger().Error("two-factor gate lookup failed", "err", err)
			next.ServeHTTP(w, r)
		case has:
			next.ServeHTTP(w, r)
		default:
			a.toSecurity(w, r)
		}
	})
}

func (a *Admin) toSecurity(w http.ResponseWriter, r *http.Request) {
	target := a.securityURL()
	if IsHTMX(r) {
		a.SetFlash(w, r, "info", "Set up two-step sign-in to continue.")
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusOK)
		return
	}
	a.SetFlash(w, r, "info", "Set up two-step sign-in to continue.")
	http.Redirect(w, r, target, http.StatusSeeOther)
}
