package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/siteconfig"
)

// fieldErr is a validation error that names the setting at fault, like the
// server's locale error.
type fieldErr struct{ field, msg string }

func (e fieldErr) Error() string       { return e.msg }
func (e fieldErr) LocaleField() string { return e.field }

// A locales error whose text mentions x-default (a page named that way) stays
// on the locales field: routing follows the reported field, not the text.
func TestSiteSettings_LocalesErrorMentioningXDefault(t *testing.T) {
	t.Parallel()
	g := newOwnerRig(t, true)
	g.a.deps.ValidateLocales = func(context.Context, string, []siteconfig.LocaleEntry, bool, string) ([]siteconfig.LocaleEntry, string, error) {
		return nil, "", fieldErr{"locales", `segment "fr" is already used by the published page "x-default landing" (/fr, id 1)`}
	}
	form := url.Values{"name": {"Acme"}, "language": {"en"}, "timezone": {"UTC"}, "locales": {"en\nfr fr\n"}}
	body := g.do("POST", "/admin/settings/general/identity", "owner-token", form, nil).Body.String()
	i := strings.Index(body, `id="locales-error"`)
	if i < 0 || !strings.Contains(body[i:], "x-default landing") {
		t.Fatalf("error is not shown on the locales field:\n%s", body)
	}
	if strings.Contains(body, `id="xdefault-error"`) {
		t.Error("error is shown on the x-default field")
	}
}

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
		return nil, "", fieldErr{"x_default_locale", `x-default locale "` + x + `" is not in the locale list`}
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

func TestLocaleSetOfKeepsHiddenAndName(t *testing.T) {
	set := localeSetOf(&siteconfig.Config{Language: "en", Locales: []siteconfig.LocaleEntry{
		{Tag: "en"}, {Tag: "fr", Segment: "fr", Name: "Francais", Hidden: true},
	}})
	if !set.IsHidden("fr") || set.Name("fr") != "Francais" || set.IsHidden("en") {
		t.Fatalf("hidden/name lost: %+v", set.Entries())
	}
}
