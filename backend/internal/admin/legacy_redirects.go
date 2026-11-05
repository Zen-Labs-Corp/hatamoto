package admin

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// legacyRule moves one old admin path (and, unless Exact, everything below it
// on a "/" boundary) to its new home. Rewrite, when set, computes the whole
// target for paths that need more than a prefix swap; it returns the new path
// and any query values to add.
type legacyRule struct {
	From, To string
	Exact    bool
	Rewrite  func(rest string) (path string, add url.Values, ok bool)
}

// legacyRulesFor is the old to new URL map of v1-23a and of the Theme area,
// under prefix. Every row answers 301 for GET and HEAD and 308 for every other
// method (so a POST keeps its method and body). Real routes win by construction:
// the table is consulted only by the admin fallback, which the mux picks when no
// real route matches. The rows are written below the prefix and joined here.
func legacyRulesFor(prefix string) []legacyRule {
	rows := []legacyRule{
		{From: "/analytics/agents", To: "/analytics/crawlers"},

		{From: "/settings/site", To: "/settings/general", Exact: true},
		{From: "/settings/branding", To: "/theme/colours", Exact: true},
		{From: "/settings/homepage", To: "/settings/general", Exact: true},
		{From: "/settings/contact", To: "/settings/general", Exact: true},
		{From: "/settings/notifications", To: "/settings/general", Exact: true},
		{From: "/settings/api", To: "/settings/api-keys"},
		{From: "/settings/plugins", To: "/plugins"},
		{From: "/settings/theme", To: "/theme/themes"},
		{From: "/settings/theme/design", To: "/theme/colours"},

		// The Theme area: Appearance and Menus left Settings for /admin/theme.
		{From: "/settings/themes", To: "/theme/themes"},
		{From: "/settings/themes/design", To: "/theme/colours"},
		{From: "/settings/themes/tokens", To: "/theme/design-system"},
		{From: "/settings/themes/presets", To: "/theme/presets"},
		{From: "/settings/themes/parts", To: "/theme/parts"},
		{From: "/settings/sections", To: "/theme/templates"},
		{From: "/settings/menus", To: "/theme/menus"},
		{From: "/settings/contact-button", To: "/theme/contact-button"},
		{From: "/settings/appearance", To: "/theme", Exact: true},
		{From: "/settings/menus-forms", To: "/theme/menus", Exact: true},

		{From: "/schemas", To: "/settings/types"},
		{From: "/taxonomies", To: "/settings/taxonomies"},
		{From: "/menus", To: "/theme/menus"},
		{From: "/redirects", To: "/settings/redirects"},
		{From: "/linking", To: "/settings/linking"},
		{From: "/sections", To: "/theme/templates"},
		{From: "/users", To: "/settings/users"},

		{From: "/import", To: "/settings/import", Exact: true},
		{From: "/import/wordpress", To: "/settings/import/wordpress"},

		// Forms left Settings for the main menu. A form's edit and delete pages
		// are /forms/edit/{id} and /forms/delete/{id} (a /forms/{id}/edit route
		// would clash with /forms/submissions/{id}).
		{From: "/settings/forms", Rewrite: legacyFormPages(prefix)},
		{From: "/settings/forms", To: "/forms"},
		{From: "/settings/submissions", To: "/forms/submissions"},
		{From: "/settings/spam", To: "/forms/spam"},
		{From: "/settings/media", To: "/media"},

		// /admin/forms/{id}/submissions...: the form id moves to the query, a
		// submission id is enough on its own.
		{From: "/forms", Rewrite: func(rest string) (string, url.Values, bool) { return legacySubmissions(prefix, rest) }},
	}
	for i := range rows {
		rows[i].From = prefix + rows[i].From
		if rows[i].To != "" {
			rows[i].To = prefix + rows[i].To
		}
	}
	return sortLegacyRules(rows)
}

// legacyFormPages rewrites {id}/edit and {id}/delete under <prefix>/settings/forms.
func legacyFormPages(prefix string) func(rest string) (string, url.Values, bool) {
	return func(rest string) (string, url.Values, bool) {
		parts := strings.Split(strings.Trim(rest, "/"), "/")
		if len(parts) != 2 || parts[0] == "" || (parts[1] != "edit" && parts[1] != "delete") {
			return "", nil, false
		}
		return prefix + "/forms/" + parts[1] + "/" + parts[0], nil, true
	}
}

// legacySubmissions rewrites {id}/submissions[/rest] under <prefix>/forms, and
// the pre-settings {id}/edit and {id}/delete to the form pages.
func legacySubmissions(prefix, rest string) (string, url.Values, bool) {
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 2 && (parts[1] == "edit" || parts[1] == "delete") && parts[0] != "" {
		return prefix + "/forms/" + parts[1] + "/" + parts[0], nil, true
	}
	if len(parts) < 2 || parts[1] != "submissions" || parts[0] == "" {
		return "", nil, false
	}
	base := prefix + "/forms/submissions"
	form := url.Values{"form": {parts[0]}}
	switch {
	case len(parts) == 2:
		return base, form, true
	case len(parts) == 3 && (parts[2] == "bulk" || parts[2] == "export.csv"):
		return base + "/" + parts[2], form, true
	default:
		return base + "/" + strings.Join(parts[2:], "/"), nil, true
	}
}

// sortLegacyRules puts rewrite rules first and the rest longest From first, so
// the most specific rule wins.
func sortLegacyRules(rs []legacyRule) []legacyRule {
	sort.SliceStable(rs, func(i, j int) bool {
		if (rs[i].Rewrite != nil) != (rs[j].Rewrite != nil) {
			return rs[i].Rewrite != nil
		}
		return len(rs[i].From) > len(rs[j].From)
	})
	return rs
}

// legacyTargetIn is the new location of an old admin URL: path and query kept,
// fragment dropped. ok is false when the path is not in the table.
//
// The target is built from the escaped path, so an encoded slash or dot stays
// inert, and a path that could climb out of the admin prefix (dot segments, backslash,
// control bytes) is not redirected at all: the caller falls through to a 404.
func legacyTargetIn(rules []legacyRule, u *url.URL) (string, bool) {
	if strings.Contains(u.Path, "..") || strings.ContainsAny(u.Path, "\\\x7f") || strings.IndexFunc(u.Path, func(r rune) bool { return r < 0x20 }) >= 0 {
		return "", false
	}
	p := strings.TrimRight(u.EscapedPath(), "/")
	for _, r := range rules {
		var to string
		var add url.Values
		switch {
		case p == r.From && r.Rewrite == nil:
			to = r.To
		case r.Rewrite != nil && strings.HasPrefix(p, r.From+"/"):
			var ok bool
			if to, add, ok = r.Rewrite(strings.TrimPrefix(p, r.From+"/")); !ok {
				continue
			}
		case !r.Exact && r.Rewrite == nil && strings.HasPrefix(p, r.From+"/"):
			to = r.To + strings.TrimPrefix(p, r.From)
		default:
			continue
		}
		q := u.Query()
		for k, v := range add {
			q[k] = v
		}
		if enc := q.Encode(); enc != "" {
			// RawQuery order is kept when nothing was added.
			if len(add) == 0 {
				enc = u.RawQuery
			}
			to += "?" + enc
		}
		return to, true
	}
	return "", false
}

// legacyRedirectFor answers 301 (GET, HEAD) or 308 (any other method) on an old
// admin URL in rules (see legacyRulesFor), before the session and CSRF checks so
// a sign-in "next" never carries an old path.
func legacyRedirectFor(rules []legacyRule, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if to, ok := legacyTargetIn(rules, r.URL); ok {
			status := http.StatusMovedPermanently
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				status = http.StatusPermanentRedirect
			}
			http.Redirect(w, r, to, status)
			return
		}
		next.ServeHTTP(w, r)
	})
}
