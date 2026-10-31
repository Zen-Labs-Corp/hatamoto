package admin

import (
	"context"
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

func TestGeneralSaveRefusesLanguageOutsideTheLocales(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	site.cfg.Language = "en"
	g.a.deps.ValidateLocales = func(_ context.Context, lang string, e []siteconfig.LocaleEntry, _ bool, x string) ([]siteconfig.LocaleEntry, string, error) {
		for _, l := range e {
			if l.Tag == lang && !l.Hidden {
				return e, x, nil
			}
		}
		return nil, "", fieldErr{"locales", "default locale " + lang + " is not a public locale"}
	}
	for _, lang := range []string{"es", "fr"} { // not in the list; hidden
		f := url.Values{"name": {"Acme"}, "language": {lang}, "timezone": {"UTC"}}
		rec := g.do("POST", "/admin/settings/general/identity", "owner-token", f, nil)
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Settings &gt; Locales") {
			t.Errorf("language %s: code=%d, want 422 pointing to Settings > Locales", lang, rec.Code)
		}
		if site.cfg.Language != "en" {
			t.Errorf("language %s: stored language = %q", lang, site.cfg.Language)
		}
	}
}
