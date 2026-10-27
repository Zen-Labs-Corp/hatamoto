package admin

import (
	"net/http"
	"strconv"

	"hatamoto.dev/core/internal/admin/ui"
)

// errorData feeds the full-page error: Code and Icon draw the status on hm/empty, Actions are
// the way back (secondary) and the optional retry (primary).
type errorData struct {
	Heading, Message, Code, Icon string
	Actions                      []ui.Button
}

// errorIcon picks the icon for a status: a missing page, a refused one, anything else.
func errorIcon(status int) string {
	switch status {
	case http.StatusNotFound:
		return "search"
	case http.StatusForbidden:
		return "lock"
	}
	return "alert"
}

// Error answers with an error page, or, for an htmx fragment request, a toast
// plus HX-Reswap: none so the current page stays put.
func (a *Admin) Error(w http.ResponseWriter, r *http.Request, status int, heading, message string) {
	a.ErrorBack(w, r, status, heading, message, a.landing(), "Back to the admin")
}

// ErrorBack is Error with a way back that suits the screen the error is on.
func (a *Admin) ErrorBack(w http.ResponseWriter, r *http.Request, status int, heading, message, back, backLabel string) {
	a.errorPage(w, r, status, heading, message, back, backLabel, "")
}

// errorPage renders the error page; retry, when set, is the address a "Try
// again" button reloads (a screen that may work in a moment).
func (a *Admin) errorPage(w http.ResponseWriter, r *http.Request, status int, heading, message, back, backLabel, retry string) {
	if isFragment(r) {
		w.Header().Set("HX-Reswap", "none")
		Toast(w, "error", fullStop(heading)+" "+fullStop(message))
		w.WriteHeader(status)
		return
	}
	v := a.NewView(w, r, heading)
	page := "errors/error"
	if v.User == nil {
		page = "errors/bare"
		back, backLabel = a.adminPath+"/login", "Go to sign in"
	}
	back1 := ui.Button{Label: backLabel, Href: back, Variant: "primary"}
	actions := []ui.Button{back1}
	if retry != "" {
		back1.Variant = ""
		actions = []ui.Button{back1, {Label: "Try again", Href: retry, Variant: "primary"}}
	}
	v.Data = errorData{Heading: heading, Message: message, Code: strconv.Itoa(status), Icon: errorIcon(status), Actions: actions}
	a.Render(w, r, status, page, v)
}
