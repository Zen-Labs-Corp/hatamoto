package admin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// IsHTMX reports an htmx-issued request .
func IsHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// isFragment is an htmx request that wants only a region, not the whole page.
func isFragment(r *http.Request) bool {
	return IsHTMX(r) && r.Header.Get("HX-Target") != ""
}

// Redirect sends the browser to target after a successful action: a 303 for
// plain requests, HX-Redirect (a full navigation) for htmx ones.
func Redirect(w http.ResponseWriter, r *http.Request, target string) {
	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// Toast sets the HX-Trigger header that makes admin.js show a toast.
func Toast(w http.ResponseWriter, kind, message string) {
	b, _ := json.Marshal(map[string]any{"hm:toast": map[string]any{"kind": kind, "message": message, "timeout": 4000}})
	w.Header().Set("HX-Trigger", string(b))
}

// SafeNextFor validates a post-login "next" target: a same-site path under the
// admin prefix only. Anything else (absolute URLs, //host, backslashes, control
// chars, other prefixes) becomes "" so the caller falls back to the default
// landing page.
//
// The sudo page is never a target, but its own next is: a sign-in from an
// expired sudo page lands on the page the owner was headed to. That unwraps
// at most twice.
func SafeNextFor(prefix, raw string) string { return safeNext(prefix, raw, 2) }

func safeNext(prefix, raw string, unwrap int) string {
	if raw == "" || len(raw) > 2048 {
		return ""
	}
	for _, c := range raw {
		if c < 0x20 || c == 0x7f || c == '\\' {
			return ""
		}
	}
	if raw != prefix && !strings.HasPrefix(raw, prefix+"/") && !strings.HasPrefix(raw, prefix+"?") {
		return ""
	}
	if strings.Contains(raw, "//") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return ""
	}
	// The wizard's address step is behind the password prompt and must come back to itself.
	if u.Path == prefix+setupAddressPath {
		return raw
	}
	// Never bounce back into the auth screens.
	for _, p := range []string{"login", "logout", "forgot", "reset", "accept", "setup", "sudo"} {
		p = urlUnder(prefix, p)
		if u.Path == p || strings.HasPrefix(u.Path, p+"/") {
			if p == urlUnder(prefix, "sudo") && u.Path == p && unwrap > 0 {
				return safeNext(prefix, u.Query().Get("next"), unwrap-1)
			}
			return ""
		}
	}
	return raw
}
