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
	// The promise is "no telemetry unless you turn it on" plus a complete, linked list of outbound
	// connections, each with its switch; updates can be turned off entirely.
	for _, want := range []string{
		"No telemetry unless you turn it on.",
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
		"three keys: `install`",
	} {
		if !strings.Contains(outbound, want) {
			t.Errorf("outbound connections page lacks the install stats detail %q", want)
		}
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
