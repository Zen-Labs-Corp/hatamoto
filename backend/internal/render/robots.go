package render

import "strings"

// defaultDisallow mirrors the Next.js frontend's default robots rules so
// switching renderers does not change crawl policy.
var defaultDisallow = []string{
	defaultAdminDisallow, "/auth", "/api", "/client", "/*?*_rsc=",
}

// defaultAdminDisallow is the admin's default address. It is the only admin
// address robots.txt may name: a configured address is never published.
const defaultAdminDisallow = "/admin"

// Robots returns the robots.txt body: allow everything except the admin and
// API surfaces, plus extra Disallow paths, and an absolute sitemap URL.
func Robots(origin string, extra []string) string {
	return RobotsWithDeny(origin, extra, nil)
}

// RobotsWithDeny is Robots plus one "User-agent: <token> / Disallow: /" group
// per denied token, placed before the Sitemap line exactly like the Next.js
// route. With no tokens the output is identical to Robots.
func RobotsWithDeny(origin string, extra, denyTokens []string) string {
	return RobotsForAdmin(origin, extra, denyTokens, defaultAdminDisallow)
}

// RobotsForAdmin is RobotsWithDeny for a site whose admin is served at
// adminPath. The admin Disallow line is written only when adminPath is exactly
// "/admin": the default is public knowledge, any other address stays out of a
// file every crawler reads.
func RobotsForAdmin(origin string, extra, denyTokens []string, adminPath string) string {
	return RobotsForAdminLines(origin, extra, nil, denyTokens, adminPath)
}

// RobotsForAdminLines is RobotsForAdmin plus the owner's extra robots.txt
// lines, written verbatim right after the built-in Disallow lines of the
// "User-agent: *" group and before the deny groups: a bare Disallow joins the
// "*" group, a User-agent line starts its own group. Empty lines and any line
// naming a non-empty adminPath are skipped, so a configured admin address is
// never published. With no lines the output equals RobotsForAdmin.
func RobotsForAdminLines(origin string, extra, lines, denyTokens []string, adminPath string) string {
	var b strings.Builder
	b.WriteString("User-agent: *\nAllow: /\n")
	for _, p := range append(append([]string{}, defaultDisallow...), extra...) {
		if p == defaultAdminDisallow && adminPath != defaultAdminDisallow {
			continue
		}
		if p = strings.TrimSpace(p); p != "" {
			b.WriteString("Disallow: " + p + "\n")
		}
	}
	wrote := false
	admin := strings.ToLower(strings.TrimSpace(adminPath))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || (admin != "" && strings.Contains(strings.ToLower(l), admin)) {
			continue
		}
		b.WriteString(l + "\n")
		wrote = true
	}
	if wrote {
		b.WriteString("\n")
	}
	for _, t := range denyTokens {
		if t = strings.TrimSpace(t); t != "" {
			b.WriteString("User-agent: " + t + "\nDisallow: /\n\n")
		}
	}
	b.WriteString("Sitemap: " + strings.TrimRight(origin, "/") + "/sitemap.xml\n")
	return b.String()
}

// SitemapXSL returns the embedded stylesheet that makes sitemaps readable in a
// browser, or nil when the theme does not ship one.
func SitemapXSL() []byte {
	b, err := defaultThemeFS.ReadFile("themes/default/assets/sitemap.xsl")
	if err != nil {
		return nil
	}
	return b
}
