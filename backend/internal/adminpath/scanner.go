package adminpath

import "strings"

// ScannerList holds the addresses that automated scanners try first.
var ScannerList = []string{
	"admin", "administrator", "admins", "adm", "wp-admin", "wp-login", "wp", "wordpress",
	"login", "signin", "sign-in", "auth", "dashboard", "dash", "cms", "backend", "backoffice",
	"manage", "manager", "management", "panel", "controlpanel", "cpanel", "cp", "console", "portal",
	"user", "users", "account", "accounts", "member", "members", "ghost", "umbraco",
	"typo3", "joomla", "drupal", "craft", "hatamoto", "hm", "editor", "staff", "secure", "private",
	"root", "sysadmin", "webadmin", "siteadmin",
	"admin-panel", "adminpanel", "admin-login", "adminlogin", "moderator", "owner", "config", "setup", "install",
}

var (
	weakPrefixes = []string{"my", "site"}
	weakSuffixes = []string{"panel", "login"}

	// scannerSet holds the list in its stripped form (no digits or dashes), the
	// form weakForm produces.
	scannerSet = func() map[string]struct{} {
		m := map[string]struct{}{}
		for _, s := range ScannerList {
			m[stripDigitsDashes(s)] = struct{}{}
		}
		return m
	}()
)

func stripDigitsDashes(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r >= '0' && r <= '9' {
			return -1
		}
		return r
	}, s)
}

// weakCandidates lists the forms of seg that are compared with the scanner
// list and the word list: digits and dashes removed, then every combination of
// the common prefixes and suffixes removed ("my-admin-panel2" gives
// "myadminpanel", "adminpanel", "myadmin", "admin"). A prefix or suffix is
// only removed when something is left.
func weakCandidates(seg string) []string {
	out := []string{stripDigitsDashes(seg)}
	seen := map[string]bool{out[0]: true}
	for i := 0; i < len(out); i++ {
		cur := out[i]
		var next []string
		for _, p := range weakPrefixes {
			if strings.HasPrefix(cur, p) && len(cur) > len(p) {
				next = append(next, cur[len(p):])
			}
		}
		for _, s := range weakSuffixes {
			if strings.HasSuffix(cur, s) && len(cur) > len(s) {
				next = append(next, cur[:len(cur)-len(s)])
			}
		}
		for _, n := range next {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

// onScannerList reports whether seg, or a stripped form of it, is on the list.
func onScannerList(seg string) bool {
	for _, c := range weakCandidates(seg) {
		if _, ok := scannerSet[c]; ok {
			return true
		}
	}
	return false
}
