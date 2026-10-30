package admin

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/siteconfig"
)

func TestParseLocales(t *testing.T) {
	got, bad := parseLocales("en\nfr fr\n\n  nl   nl \n")
	if bad != "" || len(got) != 3 || got[0].Segment != "" || got[1] != (siteconfig.LocaleEntry{Tag: "fr", Segment: "fr"}) || got[2].Tag != "nl" {
		t.Fatalf("got %v %q", got, bad)
	}
	if _, bad := parseLocales("fr fr extra"); bad == "" {
		t.Error("three words accepted")
	}
}

func TestSiteSettings_LocalesForm(t *testing.T) {
	t.Parallel()
	g := newOwnerRig(t, true)
	site := g.a.deps.Site.(*fakeSite)
	g.a.deps.ValidateLocales = func(_ context.Context, def string, e []siteconfig.LocaleEntry, _ bool, x string) ([]siteconfig.LocaleEntry, string, error) {
		for _, l := range e {
			if l.Segment == "api" {
				return nil, "", errors.New("segment \"api\" is a route of its own")
			}
		}
		return e, x, nil
	}
	base := url.Values{"name": {"Acme"}, "language": {"en"}, "timezone": {"UTC"}}
	with := func(k, v string) url.Values {
		f := url.Values{}
		for kk, vv := range base {
			f[kk] = vv
		}
		f.Set(k, v)
		return f
	}

	// A post without the locale fields leaves the stored locales alone.
	site.cfg.Locales = []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr"}}
	if rec := g.do("POST", "/admin/settings/general/identity", "owner-token", base, nil); rec.Code != 303 || len(site.cfg.Locales) != 2 {
		t.Fatalf("plain post: %d %v", rec.Code, site.cfg.Locales)
	}

	f := with("locales", "en\nnl nl\n")
	f.Set("prefix_all", "true")
	f.Set("xdefault", "en")
	if rec := g.do("POST", "/admin/settings/general/identity", "owner-token", f, nil); rec.Code != 303 ||
		len(site.cfg.Locales) != 2 || site.cfg.Locales[1].Segment != "nl" || !site.cfg.PrefixAll || site.cfg.XDefaultLocale != "en" {
		t.Fatalf("save: %d %+v", rec.Code, site.cfg)
	}
	page := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String()
	if !strings.Contains(page, ">en\nnl nl\n</textarea>") || !strings.Contains(page, `name="xdefault"`) || !strings.Contains(page, `value="en"`) {
		t.Errorf("form does not show the saved locales:\n%s", page)
	}

	rec := g.do("POST", "/admin/settings/general/identity", "owner-token", with("locales", "fr api"), nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "a route of its own") || len(site.cfg.Locales) != 2 {
		t.Fatalf("rejected: %d", rec.Code)
	}

	// An emptied field returns the site to one locale.
	if rec := g.do("POST", "/admin/settings/general/identity", "owner-token", with("locales", ""), nil); rec.Code != 303 ||
		len(site.cfg.Locales) != 0 || site.cfg.PrefixAll || site.cfg.XDefaultLocale != "" {
		t.Fatalf("clear: %d %+v", rec.Code, site.cfg)
	}
}
