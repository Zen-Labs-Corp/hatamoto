package seo

import (
	"fmt"
	"regexp"
	"strings"
)

// Limits for an ads.txt or app-ads.txt body.
const (
	MaxAdsTxtLines = 5000
	MaxAdsTxtBytes = 256 << 10
)

// AdSenseCertID is the certification authority id Google publishes for its
// ads.txt lines.
const AdSenseCertID = "f08c47fec0942fa0"

var (
	adsVariable = map[string]bool{
		"contact": true, "subdomain": true, "inventorypartnerdomain": true,
		"ownerdomain": true, "managerdomain": true,
	}
	adsDomain = regexp.MustCompile(`(?i)^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	adsCertID = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	adsPubID  = regexp.MustCompile(`^(?:ca-)?(pub-[0-9]+)$`)
)

// ValidateAdsTxt checks an ads.txt body line by line against the IAB grammar:
// comments, blank lines, the fixed variables (CONTACT=, SUBDOMAIN=, ...) and
// records "domain, account id, DIRECT|RESELLER[, certification id]". It
// reports the first bad line, as one error on field.
func ValidateAdsTxt(field, text string) []FieldError {
	bad := func(msg string) []FieldError { return []FieldError{{field, msg}} }
	if len(text) > MaxAdsTxtBytes {
		return bad(fmt.Sprintf("At most %d KiB.", MaxAdsTxtBytes>>10))
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if len(lines) > MaxAdsTxtLines {
		return bad(fmt.Sprintf("At most %d lines.", MaxAdsTxtLines))
	}
	for i, raw := range lines {
		if msg := adsLineError(raw); msg != "" {
			return bad(fmt.Sprintf("Line %d: %s", i+1, msg))
		}
	}
	return nil
}

func adsLineError(raw string) string {
	for _, r := range raw {
		if (r < 0x20 && r != '\t') || r == 0x7f {
			return "remove the control character."
		}
	}
	line, _, _ := strings.Cut(raw, "#")
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	if name, value, ok := strings.Cut(line, "="); ok {
		if !adsVariable[strings.ToLower(strings.TrimSpace(name))] {
			return "use CONTACT, SUBDOMAIN, INVENTORYPARTNERDOMAIN, OWNERDOMAIN, MANAGERDOMAIN or a record."
		}
		if strings.TrimSpace(value) == "" {
			return strings.TrimSpace(name) + " needs a value."
		}
		return ""
	}
	f := strings.Split(line, ",")
	if len(f) < 3 || len(f) > 4 {
		return "a record needs a domain, an account id, DIRECT or RESELLER, and an optional certification id."
	}
	for i := range f {
		f[i] = strings.TrimSpace(f[i])
	}
	if !adsDomain.MatchString(f[0]) {
		return "the first field must be an ad system domain such as google.com."
	}
	if f[1] == "" || strings.ContainsAny(f[1], " \t") {
		return "the account id is missing or contains spaces."
	}
	if t := strings.ToUpper(f[2]); t != "DIRECT" && t != "RESELLER" {
		return "the account type must be DIRECT or RESELLER."
	}
	if len(f) == 4 && !adsCertID.MatchString(f[3]) {
		return "the certification id may only use letters, digits and dashes."
	}
	return ""
}

// NormalizeAdsTxt trims every line, converts CRLF to LF, drops trailing blank
// lines and ends a non-empty body with one newline.
func NormalizeAdsTxt(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	out := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	if out == "" {
		return ""
	}
	return out + "\n"
}

// AdSensePubID turns a vendor id (ca-pub-123 or pub-123) into the ads.txt
// account id (pub-123), or "" when it is not a publisher id.
func AdSensePubID(vendorID string) string {
	m := adsPubID.FindStringSubmatch(strings.TrimSpace(vendorID))
	if m == nil {
		return ""
	}
	return m[1]
}

// AdSenseLine is the ads.txt record Google asks AdSense publishers to add.
func AdSenseLine(pubID string) string {
	id := AdSensePubID(pubID)
	if id == "" {
		return ""
	}
	return "google.com, " + id + ", DIRECT, " + AdSenseCertID
}
