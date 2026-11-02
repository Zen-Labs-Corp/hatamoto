package wpimport

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"hatamoto.dev/core/internal/seo"
)

// seoLive is what the site's public pages reveal about its SEO setup.
type seoLive struct {
	Plugin                string
	SiteName, TwitterSite string
	Verification          map[string]string // google|bing|yandex|baidu|pinterest|facebook
	OrgName, OrgLogo      string
	SameAs                []string
	Separator             string
	HomeTitle, PostTitle  string
	SiteTitle             string
	RobotsExtra           []string
	Noindex               map[string]bool
	Warnings              []string
}

// headSettings is the SEO-relevant content of one page's <head>.
type headSettings struct {
	Plugin, Title, SiteName, TwitterSite string
	Verification                         map[string]string
	OrgName, OrgLogo                     string
	SameAs                               []string
	Noindex                              bool
}

var (
	htmlCommentRe = regexp.MustCompile(`(?s)<!--(.*?)-->`)
	ldJSONRe      = regexp.MustCompile(`(?is)<script[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	pluginNames   = []string{"The SEO Framework", "Yoast SEO", "Rank Math", "All in One SEO", "SEOPress"}
	verifyMetas   = map[string]string{
		"google-site-verification":     "google",
		"msvalidate.01":                "bing",
		"yandex-verification":          "yandex",
		"baidu-site-verification":      "baidu",
		"p:domain_verify":              "pinterest",
		"facebook-domain-verification": "facebook",
	}
)

// parseHeadSettings reads the head of page (up to </head>).
func parseHeadSettings(page []byte) headSettings {
	s := string(page)
	if i := strings.Index(strings.ToLower(s), "</head>"); i >= 0 {
		s = s[:i]
	}
	out := headSettings{Verification: map[string]string{}}
	if m := titleRe.FindStringSubmatch(s); m != nil {
		out.Title = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	for _, m := range htmlCommentRe.FindAllStringSubmatch(s, -1) {
		for _, n := range pluginNames {
			if out.Plugin == "" && strings.Contains(strings.ToLower(m[1]), strings.ToLower(n)) {
				out.Plugin = n
			}
		}
	}
	for _, tag := range metaTagRe.FindAllString(s, -1) {
		a := attrsOf(tag)
		name, content := strings.ToLower(a["name"]), strings.TrimSpace(a["content"])
		switch {
		case strings.EqualFold(a["property"], "og:site_name"):
			out.SiteName = content
		case name == "twitter:site":
			out.TwitterSite = content
		case name == "robots":
			out.Noindex = strings.Contains(strings.ToLower(content), "noindex")
		case verifyMetas[name] != "" && content != "":
			out.Verification[verifyMetas[name]] = content
		case a["property"] != "" && verifyMetas[strings.ToLower(a["property"])] != "" && content != "":
			out.Verification[verifyMetas[strings.ToLower(a["property"])]] = content
		}
	}
	for _, m := range ldJSONRe.FindAllStringSubmatch(s, -1) {
		var v any
		if json.Unmarshal([]byte(strings.TrimSpace(m[1])), &v) != nil {
			continue
		}
		findOrganization(v, &out)
	}
	return out
}

func findOrganization(v any, out *headSettings) {
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			findOrganization(e, out)
		}
	case map[string]any:
		if g, ok := t["@graph"]; ok {
			findOrganization(g, out)
		}
		if out.OrgName != "" || !hasType(t["@type"], "Organization") {
			return
		}
		out.OrgName, _ = t["name"].(string)
		switch l := t["logo"].(type) {
		case string:
			out.OrgLogo = l
		case map[string]any:
			out.OrgLogo, _ = l["url"].(string)
		}
		switch sa := t["sameAs"].(type) {
		case string:
			out.SameAs = []string{sa}
		case []any:
			for _, e := range sa {
				if str, ok := e.(string); ok && str != "" {
					out.SameAs = append(out.SameAs, str)
				}
			}
		}
	}
}

func hasType(t any, want string) bool {
	switch v := t.(type) {
	case string:
		return v == want
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

// readSEOLive fetches the home page, one post, one archive, robots.txt and one
// sitemap from the site's own host. Failures become Warnings.
func readSEOLive(ctx context.Context, c *Client, home string, postURL, archiveURL string) *seoLive {
	live := &seoLive{Verification: map[string]string{}, Noindex: map[string]bool{}}
	fetch := func(u string) ([]byte, bool) {
		pu, err := url.Parse(u)
		if err != nil || u == "" || strings.ToLower(pu.Hostname()) != c.Host() {
			return nil, false
		}
		body, _, err := c.Do(ctx, u, 2<<20)
		if err != nil {
			live.Warnings = append(live.Warnings, fmt.Sprintf("SEO live read: %s: %v", u, err))
			return nil, false
		}
		return body, true
	}
	if body, ok := fetch(home); ok {
		h := parseHeadSettings(body)
		live.Plugin, live.SiteName, live.TwitterSite, live.HomeTitle = h.Plugin, h.SiteName, h.TwitterSite, h.Title
		live.OrgName, live.OrgLogo, live.SameAs = h.OrgName, h.OrgLogo, h.SameAs
		for k, v := range h.Verification {
			live.Verification[k] = v
		}
	}
	if body, ok := fetch(postURL); ok {
		h := parseHeadSettings(body)
		live.PostTitle = h.Title
		if live.Plugin == "" {
			live.Plugin = h.Plugin
		}
	}
	if body, ok := fetch(archiveURL); ok && parseHeadSettings(body).Noindex {
		live.Noindex["archive"] = true
	}
	live.SiteTitle = live.SiteName
	if live.SiteTitle == "" {
		live.SiteTitle = live.HomeTitle
	}
	live.Separator = inferSeparator(live.PostTitle, live.SiteTitle)

	base := strings.TrimRight(home, "/")
	if base == "" {
		base = c.Base.String()
	}
	if body, ok := fetch(base + "/robots.txt"); ok {
		live.RobotsExtra = robotsExtra(string(body))
		if sm := firstSitemap(string(body), c.Host()); sm != "" {
			fetch(sm)
		}
	}
	return live
}

func inferSeparator(postTitle, siteTitle string) string {
	if postTitle == "" || siteTitle == "" || !strings.HasSuffix(postTitle, siteTitle) {
		return ""
	}
	head := strings.TrimSuffix(postTitle, siteTitle)
	if !strings.HasSuffix(head, " ") {
		return ""
	}
	fields := strings.Fields(head)
	if len(fields) == 0 {
		return ""
	}
	sep := fields[len(fields)-1]
	for _, s := range seo.Separators {
		if s == sep {
			return s
		}
	}
	return ""
}

func firstSitemap(robots, host string) string {
	for _, line := range strings.Split(robots, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(k), "sitemap") {
			continue
		}
		v = strings.TrimSpace(v)
		if u, err := url.Parse(v); err == nil && strings.ToLower(u.Hostname()) == host {
			return v
		}
		return ""
	}
	return ""
}

// robotsExtra returns the non-default robots.txt lines, or nil when anything
// other than plain crawl directives is present.
func robotsExtra(body string) []string {
	var out []string
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			out = append(out, line)
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			return nil
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "sitemap":
			continue
		case "user-agent", "allow", "disallow", "crawl-delay":
		default:
			return nil
		}
		if strings.Contains(v, "/wp-admin/") || strings.Contains(v, "admin-ajax") {
			continue
		}
		out = append(out, line)
	}
	// Drop User-agent lines that no longer head any rule.
	var kept []string
	for i, l := range out {
		if strings.HasPrefix(strings.ToLower(l), "user-agent") {
			hasRule := false
			for _, n := range out[i+1:] {
				if strings.HasPrefix(strings.ToLower(n), "user-agent") {
					break
				}
				if !strings.HasPrefix(n, "#") {
					hasRule = true
					break
				}
			}
			if !hasRule {
				continue
			}
		}
		kept = append(kept, l)
	}
	for _, l := range kept {
		if !strings.HasPrefix(l, "#") && !strings.HasPrefix(strings.ToLower(l), "user-agent") {
			return kept
		}
	}
	return nil
}
