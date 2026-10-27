package admin

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/person"
)

// completeProfile is the one-time "Complete your profile" step under this
// instance's prefix.
func (a *Admin) completeProfile() string { return a.url("profile", "complete") }

// profileGateExempt are the paths (below the admin prefix) an incomplete user
// can still reach: the profile pages that complete it, sign out, static assets,
// and the media upload and picker the photo field needs. A prefix matches the
// path itself and everything below it ("/profile" also covers /profile/password).
var profileGateExempt = []string{
	"/profile",
	"/logout",
	"/_",
	"/media/upload",
	"/media/picker",
}

// profileGateExemptExact are exempt as themselves only: POST <prefix>/theme is
// the colour-scheme switch, while <prefix>/theme/... is the Theme area.
var profileGateExemptExact = []string{"/theme"}

func profileGateExemptedIn(prefix, full string) bool {
	path, ok := strings.CutPrefix(full, prefix)
	if !ok {
		return false
	}
	if slices.Contains(profileGateExemptExact, path) {
		return true
	}
	for _, p := range profileGateExempt {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// inSetupWizard is true for <prefix>/setup and everything under it while the
// wizard is unfinished, plus the closing <prefix>/setup/done page (the content
// step is what finishes setup, and its redirect lands there): a fresh install
// runs the wizard first and meets the profile step on the next admin page.
// Once setup is complete the other wizard paths are ordinary admin paths
// (they 404 for a complete user) and the gate applies to them.
func (a *Admin) inSetupWizard(r *http.Request) bool {
	p := r.URL.Path
	if p == a.url("setup", "done") {
		return true
	}
	setup := a.url("setup")
	return (p == setup || strings.HasPrefix(p, setup+"/")) && !a.setupCompleted(r)
}

// RequireProfile sends a signed-in user whose person has no name or no photo
// to the complete-profile step. It runs after RequireSession and never writes:
// "complete" is derived from the person, so there is no flag to keep in step.
// A GET navigation carries next= so the step ends where the user was headed;
// an htmx request gets HX-Redirect (next is the page it came from); anything
// else (a POST) is redirected without next, since replaying it is not safe.
func (a *Admin) RequireProfile(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.deps.People == nil || profileGateExemptedIn(a.adminPath, r.URL.Path) || a.inSetupWizard(r) {
			next.ServeHTTP(w, r)
			return
		}
		u := userFrom(r.Context())
		if u == nil {
			next.ServeHTTP(w, r)
			return
		}
		id, err := uuid.Parse(u.ID)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		p, err := a.deps.People.ForUser(r.Context(), id)
		switch {
		case err == nil && person.Complete(p):
			next.ServeHTTP(w, r)
		case err == nil || errors.Is(err, person.ErrNotFound):
			a.toCompleteProfile(w, r)
		default:
			// A failed lookup must not lock everyone out of the admin.
			a.logger().Error("profile gate lookup failed", "err", err)
			next.ServeHTTP(w, r)
		}
	})
}

func (a *Admin) toCompleteProfile(w http.ResponseWriter, r *http.Request) {
	var target string
	switch {
	case IsHTMX(r):
		target = a.withNext(a.headedFor(r.Header.Get("HX-Current-URL")))
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		target = a.withNext(SafeNextFor(a.adminPath, r.URL.RequestURI()))
	default:
		target = a.completeProfile()
	}
	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (a *Admin) withNext(next string) string {
	if next == "" {
		return a.completeProfile()
	}
	return a.completeProfile() + "?" + url.Values{"next": {next}}.Encode()
}

// headedFor is the admin path of an htmx request's page (HX-Current-URL is an
// absolute URL), or "" when it is not one.
func (a *Admin) headedFor(current string) string {
	u, err := url.Parse(current)
	if err != nil {
		return ""
	}
	return SafeNextFor(a.adminPath, u.RequestURI())
}
