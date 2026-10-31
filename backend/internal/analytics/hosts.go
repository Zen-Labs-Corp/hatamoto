package analytics

import (
	"slices"
	"strings"
)

// Referrer host tables for channel classification. A host matches when it
// equals an entry or is a subdomain of it. Entries with a dot are exact
// registrable hosts; bare names (google) match the label before any public
// suffix, so google.de and www.google.co.uk are covered.
var (
	searchHosts = []string{"google", "bing", "duckduckgo", "yahoo", "baidu", "yandex", "ecosia", "brave", "kagi", "startpage", "qwant"}
	socialHosts = []string{"facebook.com", "instagram.com", "x.com", "twitter.com", "t.co", "linkedin.com", "lnkd.in", "reddit.com", "pinterest.com", "youtube.com", "tiktok.com"}
	// webmailHosts must be checked before searchHosts: mail.google.com is email.
	webmailHosts = []string{"mail.google.com", "outlook.live.com", "outlook.office.com", "mail.yahoo.com"}
)

func hostIs(host string, list []string) bool {
	for _, h := range list {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// secondLevel are the second-level labels of two-part public suffixes (co.uk).
var secondLevel = []string{"co", "com", "org", "net", "ac", "gov"}

// isSearchHost matches a bare search-engine name followed by a one- or
// two-label public suffix, behind any subdomains: www.google.de and
// search.yahoo.co.jp match, google.evil.com does not.
func isSearchHost(host string) bool {
	labels := strings.Split(host, ".")
	for i, label := range labels {
		after := labels[i+1:]
		if len(after) == 0 || len(after) > 2 {
			continue
		}
		if len(after) == 2 && !slices.Contains(secondLevel, after[0]) {
			continue
		}
		if slices.Contains(searchHosts, label) {
			return true
		}
	}
	return false
}
