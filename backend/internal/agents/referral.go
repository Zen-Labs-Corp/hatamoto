package agents

import (
	"net/url"
	"slices"
	"strings"
)

// ClassifyReferral maps a Referer URL and/or utm_source value to the AI
// assistant that sent the visitor, using the registry's ai_referrers list.
// utm_source wins over the referrer. A referrer matches when its host equals a
// listed host or is a subdomain of it (so "gemini.google.com" never matches
// "www.google.com", and "evilchatgpt.com" never matches "chatgpt.com").
// Pure: no I/O, safe for concurrent use.
func (r *Registry) ClassifyReferral(referrer, utmSource string) (string, bool) {
	if r == nil {
		return "", false
	}
	if utm := strings.ToLower(strings.TrimSpace(utmSource)); utm != "" {
		for _, ref := range r.AIReferrers {
			if slices.Contains(ref.UTM, utm) {
				return ref.Source, true
			}
		}
	}
	host := referrerHost(referrer)
	if host == "" {
		return "", false
	}
	for _, ref := range r.AIReferrers {
		for _, h := range ref.Hosts {
			if host == h || strings.HasSuffix(host, "."+h) {
				return ref.Source, true
			}
		}
	}
	return "", false
}

// referrerHost returns the lowercase hostname (no port, no trailing dot) of an
// absolute http(s) referrer URL, or "" when it is empty or malformed.
func referrerHost(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
}
