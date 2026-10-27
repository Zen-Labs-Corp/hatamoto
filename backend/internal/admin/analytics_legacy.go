package admin

import (
	"net/http"
	"net/url"
	"strings"
)

// legacyTabTarget maps the old <prefix>/analytics/crawlers?tab= values to the
// section (and widget) below the prefix that now shows them. A tab that is not
// listed lands on Crawlers.
var legacyTabTarget = map[string]string{
	"": "/analytics/crawlers", "overview": "/analytics/crawlers", "agents": "/analytics/crawlers",
	"pages": "/analytics/content", "first-crawls": "/analytics/content#w-ct-first",
	"errors": "/analytics/health#w-h-fail", "spoofed": "/analytics/health#w-h-ver", "robots": "/analytics/crawlers#w-cr-list",
}

// analyticsCarry is the part of a query that follows a visitor between
// analytics URLs: the range and the compare switch.
func analyticsCarry(q url.Values) url.Values {
	out := url.Values{}
	for _, k := range []string{"from", "to", "compare"} {
		if q.Has(k) {
			out.Set(k, q.Get(k))
		}
	}
	return out
}

// withCarry appends carry to target, ahead of any #fragment.
func withCarry(target string, carry url.Values) string {
	if len(carry) == 0 {
		return target
	}
	path, frag, hasFrag := strings.Cut(target, "#")
	out := path + "?" + carry.Encode()
	if hasFrag {
		out += "#" + frag
	}
	return out
}

// legacyTabRedirect sends an old ?tab= URL to its new home (301) and reports
// whether it did. It only acts when tab is present, so the target never loops.
func legacyTabRedirect(prefix string, w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	if !q.Has("tab") {
		return false
	}
	target, ok := legacyTabTarget[q.Get("tab")]
	if !ok {
		target = legacyTabTarget[""]
	}
	if id := q.Get("agent"); q.Get("tab") == "agents" && id != "" {
		target = "/analytics/agents/" + url.PathEscape(id)
	}
	carry := analyticsCarry(q)
	switch q.Get("tab") { // the page number goes to the pager that lists these rows now
	case "pages":
		if q.Has("npage") {
			carry.Set("npage", q.Get("npage"))
		}
	case "errors":
		if q.Has("page") {
			carry.Set("fpage", q.Get("page"))
		}
	}
	http.Redirect(w, r, withCarry(prefix+target, carry), http.StatusMovedPermanently)
	return true
}

// legacyTabs serves old ?tab= URLs with a redirect and everything else with next.
func legacyTabs(prefix string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !legacyTabRedirect(prefix, w, r) {
			next(w, r)
		}
	}
}

// redirect301 moves a retired analytics URL to target, keeping the range and
// compare switch.
func redirect301(target string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, withCarry(target, analyticsCarry(r.URL.Query())), http.StatusMovedPermanently)
	}
}

// analyticsSectionSelect serves the no-JS section select of the sub-sidebar:
// /admin/analytics?section=ai-health goes to that section (303), keeping the
// range and compare switch. Without it next serves the Overview.
func (a *Admin) analyticsSectionSelect(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if !q.Has("section") {
			next(w, r)
			return
		}
		href := areaHref(a.adminPath, analyticsArea, userFrom(r.Context()).Role, q.Get("section"))
		if href == "" {
			a.forbidden(w, r)
			return
		}
		http.Redirect(w, r, withCarry(href, analyticsCarry(q)), http.StatusSeeOther)
	}
}
