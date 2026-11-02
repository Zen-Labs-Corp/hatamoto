package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/siteconfig"
)

// The Business details tab on a multi-locale site (default card first, then a
// card that does not inherit one field) and on a single-language site (one
// card). Regenerate with -update.
func TestLocalesBusinessGolden(t *testing.T) {
	var out strings.Builder
	for _, c := range []struct {
		name    string
		entries []siteconfig.LocaleEntry
	}{{"multi", threeLocales}, {"single", nil}} {
		name, entries := c.name, c.entries
		g, site := localeRig(t, entries...)
		site.cfg.CompanyName = "Example Co"
		site.cfg.Phone = "555 0100"
		site.cfg.PostalAddress = []byte(`{"street":"1 Example Street","city":"Exampleton"}`)
		site.cfg.BusinessProfile = []byte(`{"tax_id":"TX-1","office":true,"hours_note":"Mon to Fri"}`)
		if name == "multi" {
			site.cfg.LocaleProfiles = []byte(`{"aa":{"hours_note":"","no_inherit":["hours_note"]}}`)
		}
		body := g.do("GET", "/admin/settings/locales/business", "owner-token", nil, nil).Body.String()
		out.WriteString("<!-- " + name + " -->\n" + normalizePage(strings.ReplaceAll(body, g.keyID.String(), "KEY-ID")) + "\n")
	}
	compareGolden(t, "pages/settings-locales-business.html", out.String())
}
