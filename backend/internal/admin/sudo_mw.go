package admin

import (
	"net/http"
	"net/url"
)

// RequireSudo lets a request through only when its session confirmed the
// password in the last sudoTTL. Put it after RequireSession. Anyone else is sent
// to the confirmation page; a POST is never replayed, so the user submits the
// form again once confirmed.
func (a *Admin) RequireSudo(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := sessionFrom(r.Context()); s != nil && a.sudo.store.Active(s.ID) {
			next.ServeHTTP(w, r)
			return
		}
		a.toSudo(w, r)
	})
}

// toSudo mirrors toLogin. A GET comes back to itself after confirming. For any
// other method the target is the page the form was on (a same-host Referer), or
// the admin home.
func (a *Admin) toSudo(w http.ResponseWriter, r *http.Request) {
	var target string
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		target = a.sudoURL(SafeNextFor(a.adminPath, r.URL.RequestURI()), false)
	} else {
		target = a.sudoURL(a.refererNext(r), true)
	}
	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// refererNext is the admin path of a same-host Referer, or "" when there is none.
func (a *Admin) refererNext(r *http.Request) string {
	u, err := url.Parse(r.Referer())
	if err != nil || u.Host != r.Host {
		return ""
	}
	return SafeNextFor(a.adminPath, u.RequestURI())
}
