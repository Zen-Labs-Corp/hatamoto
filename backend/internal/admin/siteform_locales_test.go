package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/siteconfig"
)

func TestGeneralSaveLeavesLocalesUntouched(t *testing.T) {
	t.Parallel()
	g := newOwnerRig(t, true)
	site := g.a.deps.Site.(*fakeSite)
	site.cfg.Locales = []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr", Name: "Francais", Hidden: true}}
	site.cfg.PrefixAll, site.cfg.XDefaultLocale = true, "en"
	f := url.Values{"name": {"Acme"}, "language": {"en"}, "timezone": {"UTC"}, "locales": {"en"}, "prefix_all": {""}, "xdefault": {""}}
	if rec := g.do("POST", "/admin/settings/general/identity", "owner-token", f, nil); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	if l := site.cfg.Locales; len(l) != 2 || !l[1].Hidden || l[1].Name != "Francais" || !site.cfg.PrefixAll || site.cfg.XDefaultLocale != "en" {
		t.Errorf("General save changed locales: %+v", site.cfg)
	}
}

func TestGeneralSingleLocaleShowsAddLanguagesLink(t *testing.T) {
	t.Parallel()
	g := newOwnerRig(t, true)
	body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Add languages") || !strings.Contains(body, `href="/admin/settings/locales"`) || strings.Contains(body, `name="locales"`) {
		t.Error("General lacks the Add languages link or still has the locales field")
	}
}
