package admin

import (
	"net/http"
	"net/url"
)

// sudoPath is the address of the password confirmation page.
func (a *Admin) sudoPath() string { return a.url("sudo") }

// sudoURL is the confirmation page with the place to return to. resubmit asks
// the page to tell the user to submit their form again (a POST is never replayed).
func (a *Admin) sudoURL(next string, resubmit bool) string {
	q := url.Values{}
	if next != "" {
		q.Set("next", next)
	}
	if resubmit {
		q.Set("resubmit", "1")
	}
	if len(q) == 0 {
		return a.sudoPath()
	}
	return a.sudoPath() + "?" + q.Encode()
}

// sudoNext is where to go after confirming: the validated next parameter, never
// the confirmation page itself (SafeNextFor never returns it), else the admin home.
func (a *Admin) sudoNext(r *http.Request) string {
	n := SafeNextFor(a.adminPath, r.FormValue("next"))
	if n == "" {
		return a.url()
	}
	return n
}
