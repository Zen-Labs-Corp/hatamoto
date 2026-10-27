package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// DemoBanner is the fixed notice shown in the admin and on public pages of a
// public demo (HATAMOTO_DEMO=1).
const DemoBanner = "Public demo, resets every hour"

// DemoConfig is the public demo mode: HATAMOTO_DEMO=1 turns the instance into
// a throwaway showcase that anyone can sign in to. It refuses everything that
// could reach outside the box or lock the next visitor out (uploads, outbound
// settings, user and key management, updates). Unset, it changes nothing.
type DemoConfig struct {
	// Enabled is HATAMOTO_DEMO=1 (or true).
	Enabled bool
	// LoginEmail and LoginPassword are HATAMOTO_DEMO_LOGIN_EMAIL and
	// HATAMOTO_DEMO_LOGIN_PASSWORD: the public owner login shown on the sign-in
	// page. They come from the environment only; nothing is built in.
	LoginEmail, LoginPassword string
	// MediaQuotaMB caps the total size of the media directory (originals and
	// variants): HATAMOTO_DEMO_MEDIA_QUOTA_MB, default 200.
	MediaQuotaMB int
	// WriteBudgetMB caps the request bodies of all write requests (content,
	// uploads, API) between two restarts, so a visitor cannot grow the database
	// without bound: HATAMOTO_DEMO_WRITE_BUDGET_MB, default 150.
	WriteBudgetMB int
	// WritesPerSecond and WriteBurst are the per-address rate of write requests
	// (every non-GET): HATAMOTO_DEMO_WRITES_PER_SECOND (default 2), burst 20.
	WritesPerSecond float64
	WriteBurst      int
}

// LoadDemoConfig reads the demo env vars. In demo mode it also sets
// EMAIL_DISABLED=true, the existing kill switch, so no mail leaves the box
// whatever SMTP settings exist.
//
// HATAMOTO_DEMO is on for 1, true, yes or on, and off for 0, false, no, off or
// empty/unset (any case). Any other value is an error, so a typo cannot start
// an instance in a mode the operator did not mean.
func LoadDemoConfig() (DemoConfig, error) {
	var on bool
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv("HATAMOTO_DEMO"))); v {
	case "", "0", "false", "no", "off":
	case "1", "true", "yes", "on":
		on = true
	default:
		return DemoConfig{}, fmt.Errorf("invalid HATAMOTO_DEMO %q: use 1/true/yes/on to turn the demo on, 0/false/no/off (or leave it unset) to turn it off", os.Getenv("HATAMOTO_DEMO"))
	}
	d := DemoConfig{Enabled: on}
	if !on {
		return d, nil
	}
	d.MediaQuotaMB = positiveInt("HATAMOTO_DEMO_MEDIA_QUOTA_MB", 200)
	d.WriteBudgetMB = positiveInt("HATAMOTO_DEMO_WRITE_BUDGET_MB", 150)
	d.WritesPerSecond = float64(positiveInt("HATAMOTO_DEMO_WRITES_PER_SECOND", 2))
	d.WriteBurst = 20
	d.LoginEmail = strings.TrimSpace(os.Getenv("HATAMOTO_DEMO_LOGIN_EMAIL"))
	d.LoginPassword = os.Getenv("HATAMOTO_DEMO_LOGIN_PASSWORD")
	_ = os.Setenv("EMAIL_DISABLED", "true")
	return d, nil
}

// positiveInt reads a positive integer from the environment; anything else
// gives the default.
func positiveInt(key string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil && n > 0 {
		return n
	}
	return def
}

// ShowLogin reports whether the sign-in page shows the public demo login.
func (d DemoConfig) ShowLogin() bool {
	return d.Enabled && d.LoginEmail != "" && d.LoginPassword != ""
}

type demoRule struct {
	methods string // space separated, upper case
	path    *regexp.Regexp
	msg     string
}

func rule(methods, path, msg string) demoRule {
	return demoRule{methods: " " + methods + " ", path: regexp.MustCompile("^(?:" + path + ")$"), msg: msg}
}

const (
	msgUpload   = "Uploading plugins and themes, and allowing unsigned packages, is switched off in the public demo. Install first-party packages from the registry instead."
	msgUsers    = "Users cannot be created, invited, changed or removed in the public demo."
	msgPassword = "Passwords and email addresses cannot be changed in the public demo."
	msgKeys     = "API keys cannot be created in the public demo."
	msgOutbound = "This setting sends data to other servers (webhooks, email, captcha, imports), so it is switched off in the public demo."
	msgBackups  = "Backups and exports copy the whole site to disk, so they are switched off in the public demo."
	msgUpdates  = "Updates are switched off in the public demo."
)

// demoRules are the routes a public demo refuses. They are matched on method
// and path in one place, so a new route is not silently allowed: deny by
// listing here. Rules match the decoded path, so a %2F inside an {id} segment
// reaches the handler as a longer path than the route's pattern: the rules use
// ".+" for ids rather than "[^/]+", so they over-match (safe: they only deny)
// instead of missing it. Package installs are not listed: they are limited to
// first-party packages where the package manager decides (packages.Options).
var demoRules = []demoRule{
	rule("POST", `/admin/plugins/(upload|unsigned)`, msgUpload),
	rule("POST", `/admin/settings/users(/.*)?`, msgUsers),
	rule("POST PATCH DELETE", `/api/v1/users(/.*)?`, msgUsers),
	rule("POST", `/admin/profile/password|/admin/forgot|/admin/reset|/admin/accept`, msgPassword),
	rule("PATCH", `/api/v1/me`, msgPassword),
	rule("POST", `/admin/settings/api-keys|/admin/settings/api-keys/.+/regenerate`, msgKeys),
	rule("POST PATCH", `/api/v1/api-keys(/.*)?`, msgKeys),
	rule("POST", `/admin/settings/(webhooks|email|spam)(/.*)?|/admin/setup/email|/admin/settings/import/.*`, msgOutbound),
	rule("POST", `/admin/settings/updates(/.*)?`, msgUpdates),
	rule("POST", `/admin/settings/backups(/.*)?`, msgBackups),
	rule("GET", `/admin/settings/backups/.+/download`, msgBackups),
}

// Refusal says whether a public demo refuses the request, and why. It is
// always false when the demo is off.
func (d DemoConfig) Refusal(method, path string) (string, bool) {
	if !d.Enabled {
		return "", false
	}
	for _, r := range demoRules {
		if strings.Contains(r.methods, " "+method+" ") && r.path.MatchString(path) {
			return r.msg, true
		}
	}
	return "", false
}
