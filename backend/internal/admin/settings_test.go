package admin

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/siteconfig"
)

// A new locale and that same locale as x-default go through one save; the
// x-default is validated against the posted list, not the stored one.
func TestSiteSettings_XDefaultInSameSave(t *testing.T) {
	t.Parallel()
	g := newOwnerRig(t, true)
	site := g.a.deps.Site.(*fakeSite)
	g.a.deps.ValidateLocales = func(_ context.Context, _ string, e []siteconfig.LocaleEntry, _ bool, x string) ([]siteconfig.LocaleEntry, string, error) {
		if x == "" {
			return e, x, nil
		}
		for _, l := range e {
			if l.Tag == x {
				return e, x, nil
			}
		}
		return nil, "", errors.New(`x-default locale "` + x + `" is not in the locale list`)
	}
	form := func(xdef string) url.Values {
		return url.Values{"name": {"Acme"}, "language": {"en"}, "timezone": {"UTC"}, "locales": {"en\nfr fr\n"}, "xdefault": {xdef}}
	}

	if rec := g.do("POST", "/admin/settings/general/identity", "owner-token", form("fr"), nil); rec.Code != 303 || site.cfg.XDefaultLocale != "fr" || len(site.cfg.Locales) != 2 {
		t.Fatalf("same-save x-default: %d %+v", rec.Code, site.cfg)
	}

	rec := g.do("POST", "/admin/settings/general/identity", "owner-token", form("pt"), nil)
	body := rec.Body.String()
	if rec.Code != 422 || site.cfg.XDefaultLocale != "fr" {
		t.Fatalf("bad x-default: %d %q", rec.Code, site.cfg.XDefaultLocale)
	}
	// The message sits on the x-default field, not on the locales field.
	i := strings.Index(body, `id="xdefault"`)
	if i < 0 || !strings.Contains(body[i:], "is not in the locale list") {
		t.Errorf("error is not shown on the x-default field:\n%s", body)
	}
	if k := strings.Index(body, `id="locales"`); k >= 0 && k < i && strings.Contains(body[k:i], "is not in the locale list") {
		t.Errorf("error is shown on the locales field")
	}
}
