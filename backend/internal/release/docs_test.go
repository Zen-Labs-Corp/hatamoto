package release

import (
	"os"
	"strings"
	"testing"
)

func readDoc(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestUpdatePromiseDocs pins the privacy promise in the README (no telemetry
// unless turned on, every outbound connection listed), the
// self-hoster guide in docs/UPGRADES.md, and the absence of wording that
// would break the promise.
func TestUpdatePromiseDocs(t *testing.T) {
	readme := readDoc(t, "../../../README.md")
	// The promise is "install stats are on by default, one switch turns them off" plus a complete, linked list of outbound
	// connections, each with its switch; updates can be turned off entirely.
	for _, want := range []string{
		"Install stats are on by default and one switch turns them off.",
		"`HATAMOTO_TELEMETRY=off`",
		"[Outbound connections](docs/site/operate/outbound-connections.md)",
		"`off` (no network call at all)",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("README lacks the outbound promise %q", want)
		}
	}
	if _, err := os.Stat("../../../docs/site/operate/outbound-connections.md"); err != nil {
		t.Errorf("the outbound connections page is missing: %v", err)
	}
	outbound := readDoc(t, "../../../docs/site/operate/outbound-connections.md")
	for _, want := range []string{
		"| Install stats |",
		"https://hatamoto.dev/p/stats/v1/ping",
		"hatamoto-stats",
		"HATAMOTO_TELEMETRY=off",
		"three keys, `install`",
	} {
		if !strings.Contains(outbound, want) {
			t.Errorf("outbound connections page lacks the install stats detail %q", want)
		}
	}
	// The report carries the feature flags and, for a site on an IP address,
	// the public IP literal as host: the pages say so and drop the old promise.
	stats := readDoc(t, "../../../docs/site/operate/install-stats.md")
	for _, key := range []string{"comments_on", "static_mode", "sso_on", "locales_bucket", "forms_in_use",
		"offsite_backups_on", "csp_enforced", "wp_imported"} {
		if !strings.Contains(stats, "`"+key+"`") {
			t.Errorf("install-stats.md does not list %q", key)
		}
	}
	if !strings.Contains(stats, "public IP address") {
		t.Error("install-stats.md must disclose the public IP address host")
	}
	for _, gone := range []string{"no IP address is collected", "**Got it**"} {
		if strings.Contains(stats, gone) {
			t.Errorf("install-stats.md still says %q", gone)
		}
	}
	if !strings.Contains(outbound, "public IP address") || !strings.Contains(outbound, "which features are on") {
		t.Error("outbound connections page must disclose the IP-literal host and the feature flags")
	}
	if strings.Contains(outbound, "No machine hostname, IP address") {
		t.Error("outbound page still promises no IP address")
	}
	before := readDoc(t, "../../../docs/site/getting-started/before-you-install.md")
	if !strings.Contains(before, "which features are on") {
		t.Error("before-you-install must mention the feature flags")
	}
	if !strings.Contains(readme, "docs/UPGRADES.md") {
		t.Errorf("README does not link to docs/UPGRADES.md")
	}
	up := readDoc(t, "../../../docs/UPGRADES.md")
	for _, h := range []string{"## Automatic updates (self-hosters)", "### Control it", "### Support policy", "### Trust and keys"} {
		if !strings.Contains(up, h) {
			t.Errorf("UPGRADES.md lacks %q", h)
		}
	}
	if strings.Contains(up, "Added by the self-hoster guide") {
		t.Error("UPGRADES.md still has the placeholder line")
	}
	if strings.Contains(strings.ToLower(readme+up), "phone-home") {
		t.Error("docs must not say phone-home")
	}
}
