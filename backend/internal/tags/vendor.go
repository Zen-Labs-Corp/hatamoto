// Package tags holds the vendor tag table behind Settings > Tracking: which
// third-party tags the core script can load, how their ids look, which
// consent category they belong to and which hosts the page CSP must allow.
// It is a leaf package: render, server and admin import it, it imports only
// consent for the category names.
package tags

import (
	"net/url"
	"regexp"
	"strings"
)

// Field describes an extra id a vendor takes next to its main id.
type Field struct {
	Label   string
	Pattern *regexp.Regexp
	Help    string
}

// Vendor is one tag the core script can load. The table is data: the public
// script, the CSP and the admin page all read it.
type Vendor struct {
	ID      string
	Label   string
	IDLabel string
	Pattern *regexp.Regexp // whole-value, anchored
	Fold    bool           // upper-case the id before validating (ids are case-insensitive)
	Aux     *Field         // optional second field
	// Category is the consent category, one of consent.Categories.
	Category string
	// Src is the loader URL template; {id} is the only placeholder. May be empty.
	Src string
	// Boot is the key of the vendor's bootstrap function in hm_tags.js.
	Boot string
	// CSP hosts the vendor needs, per directive: "https://x.com",
	// "https://*.x.com" or "wss://x.com". Images and frames are already
	// allowed over https by the base policy.
	Script, Connect, Style, Font []string
	// Noscript is a template with {id}, emitted only when no visitor can be
	// asked for consent. Empty for most vendors.
	Noscript string
	Help     string
}

// Hosts is a set of CSP origins per directive.
type Hosts struct {
	Script, Connect, Style, Font []string
}

// Vendors lists the supported tags in display order. Each entry names the
// vendor's install page its hosts were taken from.
var Vendors = []Vendor{
	{
		// https://developers.google.com/tag-platform/tag-manager/csp
		ID: "ga4", Label: "Google Analytics 4", IDLabel: "Measurement ID",
		Pattern: regexp.MustCompile(`^(G|GT)-[A-Z0-9]{4,16}$`), Fold: true,
		Category: "analytics", Boot: "ga4",
		Src:     "https://www.googletagmanager.com/gtag/js?id={id}",
		Script:  []string{"https://www.googletagmanager.com"},
		Connect: []string{"https://www.googletagmanager.com", "https://*.google-analytics.com", "https://*.analytics.google.com"},
		Help:    "Looks like G-ABC123XYZ9.",
	},
	{
		// https://developers.google.com/tag-platform/tag-manager/web
		ID: "gtm", Label: "Google Tag Manager", IDLabel: "Container ID",
		Pattern: regexp.MustCompile(`^GTM-[A-Z0-9]{4,12}$`), Fold: true,
		Category: "marketing", Boot: "gtm",
		Src:      "https://www.googletagmanager.com/gtm.js?id={id}",
		Script:   []string{"https://www.googletagmanager.com"},
		Noscript: `<iframe src="https://www.googletagmanager.com/ns.html?id={id}" height="0" width="0" hidden></iframe>`,
		Help:     "Looks like GTM-ABC123. Tags the container loads from other hosts are blocked by the page policy; add them as their own row here.",
	},
	{
		// https://support.google.com/google-ads/answer/7548399
		ID: "gads", Label: "Google Ads", IDLabel: "Conversion ID",
		Pattern: regexp.MustCompile(`^AW-[0-9]{6,12}$`), Fold: true,
		Aux:      &Field{Label: "Conversion label", Pattern: regexp.MustCompile(`^[A-Za-z0-9_-]{4,40}$`), Help: "Optional. Sends a form conversion to this label."},
		Category: "marketing", Boot: "gads",
		Src:     "https://www.googletagmanager.com/gtag/js?id={id}",
		Script:  []string{"https://www.googletagmanager.com", "https://www.googleadservices.com", "https://googleads.g.doubleclick.net"},
		Connect: []string{"https://www.google.com", "https://www.googleadservices.com", "https://*.doubleclick.net"},
		Help:    "Looks like AW-123456789.",
	},
	{
		// https://developers.facebook.com/docs/meta-pixel/get-started
		ID: "meta", Label: "Meta Pixel", IDLabel: "Pixel ID",
		Pattern:  regexp.MustCompile(`^[0-9]{10,20}$`),
		Category: "marketing", Boot: "meta",
		Src:      "https://connect.facebook.net/en_US/fbevents.js",
		Script:   []string{"https://connect.facebook.net"},
		Connect:  []string{"https://www.facebook.com", "https://connect.facebook.net"},
		Noscript: `<img height="1" width="1" hidden alt="" src="https://www.facebook.com/tr?id={id}&amp;ev=PageView&amp;noscript=1">`,
		Help:     "A 15 or 16 digit number.",
	},
	{
		// https://learn.microsoft.com/en-us/clarity/setup-and-installation/clarity-setup
		ID: "clarity", Label: "Microsoft Clarity", IDLabel: "Project ID",
		Pattern:  regexp.MustCompile(`^[a-z0-9]{8,12}$`),
		Category: "analytics", Boot: "clarity",
		Src:     "https://www.clarity.ms/tag/{id}",
		Script:  []string{"https://*.clarity.ms"},
		Connect: []string{"https://*.clarity.ms", "https://c.bing.com"},
		Help:    "Looks like k1a2b3c4d5.",
	},
	{
		// https://help.ads.microsoft.com/#apex/ads/en/56688/2
		ID: "uet", Label: "Microsoft Advertising (UET)", IDLabel: "Tag ID",
		Pattern:  regexp.MustCompile(`^[0-9]{6,12}$`),
		Category: "analytics", Boot: "uet",
		Src:     "https://bat.bing.com/bat.js",
		Script:  []string{"https://bat.bing.com"},
		Connect: []string{"https://bat.bing.com", "https://bat.bing.net"},
		Help:    "The numeric UET tag id.",
	},
	{
		// https://www.linkedin.com/help/lms/answer/a418880
		ID: "linkedin", Label: "LinkedIn Insight Tag", IDLabel: "Partner ID",
		Pattern:  regexp.MustCompile(`^[0-9]{5,12}$`),
		Aux:      &Field{Label: "Conversion ID", Pattern: regexp.MustCompile(`^[0-9]{4,12}$`), Help: "Optional. Counts a form submit as this conversion."},
		Category: "analytics", Boot: "linkedin",
		Src:     "https://snap.licdn.com/li.lms-analytics/insight.min.js",
		Script:  []string{"https://snap.licdn.com"},
		Connect: []string{"https://px.ads.linkedin.com", "https://www.linkedin.com"},
		Help:    "The numeric partner id.",
	},
	{
		// https://business-api.tiktok.com/portal/docs?id=1739585696931842
		ID: "tiktok", Label: "TikTok Pixel", IDLabel: "Pixel ID",
		Pattern: regexp.MustCompile(`^[A-Z0-9]{10,30}$`), Fold: true,
		Category: "marketing", Boot: "tiktok",
		Src:     "https://analytics.tiktok.com/i18n/pixel/events.js?sdkid={id}&lib=ttq",
		Script:  []string{"https://analytics.tiktok.com"},
		Connect: []string{"https://analytics.tiktok.com", "https://*.tiktok.com"},
		Help:    "Looks like C1A2B3D4E5F6G7H8I9J0.",
	},
	{
		// https://help.pinterest.com/en/business/article/install-the-pinterest-tag
		ID: "pinterest", Label: "Pinterest Tag", IDLabel: "Tag ID",
		Pattern:  regexp.MustCompile(`^[0-9]{10,20}$`),
		Category: "marketing", Boot: "pinterest",
		Src:     "https://s.pinimg.com/ct/core.js",
		Script:  []string{"https://s.pinimg.com"},
		Connect: []string{"https://ct.pinterest.com", "https://*.pinterest.com"},
		Help:    "A 13 digit number.",
	},
	{
		// https://help.tawk.to/article/content-security-policy
		ID: "tawk", Label: "Tawk.to chat", IDLabel: "Property and widget ID",
		Pattern:  regexp.MustCompile(`^[0-9a-f]{24}/[0-9a-z]{6,12}$`),
		Category: "functional", Boot: "tawk",
		Src:     "https://embed.tawk.to/{id}",
		Script:  []string{"https://embed.tawk.to", "https://*.tawk.to", "https://cdn.jsdelivr.net"},
		Connect: []string{"https://*.tawk.to", "wss://*.tawk.to"},
		Style:   []string{"https://*.tawk.to", "https://cdn.jsdelivr.net", "https://fonts.googleapis.com"},
		Font:    []string{"https://*.tawk.to", "https://cdn.jsdelivr.net", "https://fonts.gstatic.com"},
		Help:    "Looks like 5f0a1b2c3d4e5f6a7b8c9d0e/1abcdefgh.",
	},
	{
		// https://docs.crisp.chat/guides/others/whitelisting-crisp-domains/
		ID: "crisp", Label: "Crisp chat", IDLabel: "Website ID",
		Pattern:  regexp.MustCompile(`^(?i:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`),
		Category: "functional", Boot: "crisp",
		Src:     "https://client.crisp.chat/l.js",
		Script:  []string{"https://client.crisp.chat"},
		Connect: []string{"https://client.crisp.chat", "https://storage.crisp.chat", "wss://client.relay.crisp.chat", "wss://stream.relay.crisp.chat"},
		Style:   []string{"https://client.crisp.chat"},
		Font:    []string{"https://client.crisp.chat"},
		Help:    "A UUID.",
	},
	{
		// https://knowledge.hubspot.com/domains-and-urls/what-domains-do-i-need-to-allow-for-hubspot-tracking
		ID: "hubspot", Label: "HubSpot", IDLabel: "Hub ID",
		Pattern:  regexp.MustCompile(`^[0-9]{5,12}$`),
		Category: "marketing", Boot: "hubspot",
		Src:     "https://js.hs-scripts.com/{id}.js",
		Script:  []string{"https://js.hs-scripts.com", "https://js.hs-analytics.net", "https://js.hscollectedforms.net", "https://js.usemessages.com", "https://js.hs-banner.com"},
		Connect: []string{"https://api.hubapi.com", "https://forms.hubspot.com", "https://track.hubspot.com", "https://js.hscollectedforms.net"},
		Help:    "The numeric portal id.",
	},
	{
		// https://support.google.com/adsense/answer/9274025
		ID: "adsense", Label: "Google AdSense", IDLabel: "Publisher ID",
		Pattern:  regexp.MustCompile(`^ca-pub-[0-9]{10,20}$`),
		Category: "marketing", Boot: "adsense",
		Src:     "https://pagead2.googlesyndication.com/pagead/js/adsbygoogle.js?client={id}",
		Script:  []string{"https://pagead2.googlesyndication.com", "https://*.googlesyndication.com", "https://www.googletagservices.com", "https://adservice.google.com", "https://partner.googleadservices.com"},
		Connect: []string{"https://pagead2.googlesyndication.com", "https://googleads.g.doubleclick.net", "https://*.googlesyndication.com"},
		Help:    "Looks like ca-pub-1234567890123456.",
	},
}

// ByID returns the vendor with the given id.
func ByID(id string) (Vendor, bool) {
	for _, v := range Vendors {
		if v.ID == id {
			return v, true
		}
	}
	return Vendor{}, false
}

// NormalizeID trims id and upper-cases it where the vendor's ids are
// case-insensitive.
func (v Vendor) NormalizeID(id string) string {
	id = strings.TrimSpace(id)
	if v.Fold {
		id = strings.ToUpper(id)
	}
	return id
}

// ValidID reports whether a normalized id matches the vendor's pattern.
func (v Vendor) ValidID(id string) bool { return v.Pattern.MatchString(id) }

// idEscaper covers the characters PathEscape leaves alone but a query value needs.
var idEscaper = strings.NewReplacer("&", "%26", "=", "%3D", "+", "%2B")

// Expand substitutes {id} in a Src or Noscript template. Each path segment
// is escaped; validated ids never need it, so this is a second line of defense.
func Expand(tmpl, id string) string {
	parts := strings.Split(id, "/")
	for i, p := range parts {
		parts[i] = idEscaper.Replace(url.PathEscape(p))
	}
	return strings.ReplaceAll(tmpl, "{id}", strings.Join(parts, "/"))
}
