package analytics

import (
	"strings"

	"hatamoto.dev/core/internal/agents"
)

// VisitDefinition is the one-line definition of a visit shown in the admin
// help. The beacon (hm_track.js) implements it: no cookies, a tab-scoped
// marker decides which pageview is an entry.
const VisitDefinition = "A visit is the first pageview of a tab or an arrival from another site."

// NormalizeHost lowercases a host and strips "www.", a port and a trailing dot.
func NormalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if i := strings.LastIndexByte(h, ':'); i >= 0 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	h = strings.TrimSuffix(h, ".")
	return strings.TrimPrefix(h, "www.")
}

// Classify attributes a visit to a channel and a source. UTM parameters win
// over the referrer. Rules, in order:
//
//  1. paid: gclid/msclkid flag or medium cpc|ppc|paid*|display
//  2. email: medium email|e-mail|newsletter
//  3. social: medium social|social-network|sm
//  4. organic: medium organic
//  5. AI assistants: the registry's ai_referrers (host or utm_source)
//  6. referrer host in the search, social or webmail tables
//  7. any other external host, or a utm_source nothing above matched: referral
//  8. nothing: direct
//
// The source is utm_source, else the referrer host, else "(none)". A referrer
// equal to selfHost is internal: Classify returns an empty channel and the
// caller counts a pageview only.
func Classify(ev Event, reg *agents.Registry, selfHost string) (channel, source string) {
	ref := NormalizeHost(ev.Ref)
	if ref != "" && ref == NormalizeHost(selfHost) {
		return "", ""
	}
	utmSource := strings.ToLower(strings.TrimSpace(ev.UTMSource))
	medium := strings.ToLower(strings.TrimSpace(ev.UTMMedium))

	source = utmSource
	if source == "" {
		source = ref
	}
	if source == "" {
		source = SourceNone
	}

	switch {
	case ev.Paid, medium == "cpc", medium == "ppc", medium == "display", strings.HasPrefix(medium, "paid"):
		return ChannelPaid, source
	case medium == "email", medium == "e-mail", medium == "newsletter":
		return ChannelEmail, source
	case medium == "social", medium == "social-network", medium == "sm":
		return ChannelSocial, source
	case medium == "organic":
		return ChannelOrganic, source
	}

	refURL := ""
	if ref != "" {
		refURL = "https://" + ref + "/"
	}
	if src, ok := reg.ClassifyReferral(refURL, utmSource); ok {
		return ChannelAI, src
	}
	if ref != "" {
		switch {
		case hostIs(ref, webmailHosts):
			return ChannelEmail, source
		case isSearchHost(ref):
			return ChannelOrganic, source
		case hostIs(ref, socialHosts):
			return ChannelSocial, source
		}
		return ChannelReferral, source
	}
	if utmSource != "" {
		return ChannelReferral, source
	}
	return ChannelDirect, source
}
