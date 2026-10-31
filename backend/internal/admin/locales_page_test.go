package admin

import (
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/siteconfig"
)

func localeFixture(t *testing.T) (*ownerRig, *fakeSite) {
	t.Helper()
	g := newOwnerRig(t, true)
	site := g.a.deps.Site.(*fakeSite)
	site.cfg.Locales = []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr", Name: "Francais", Hidden: true}}
	g.a.deps.ValidateLocales = func(_ context.Context, _ string, e []siteconfig.LocaleEntry, _ bool, x string) ([]siteconfig.LocaleEntry, string, error) {
		for _, l := range e {
			if l.Hidden && l.Tag == "en" {
				return nil, "", fieldErr{"locales", "the default locale cannot be hidden"}
			}
		}
		return e, x, nil
	}
	return g, site
}

func TestSaveLocalesInvalidates(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	cfg, _ := site.Get(context.Background())
	entries := []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr", Name: "Francais"}}
	errs, err := g.a.saveLocales(context.Background(), cfg, entries, false, "")
	if err != nil || len(errs) != 0 {
		t.Fatalf("errs=%v err=%v", errs, err)
	}
	if g.flushed != 1 {
		t.Errorf("rendered pages cleared %d times, want 1", g.flushed)
	}
	if site.cfg.Locales[1].Hidden || site.cfg.Locales[1].Name != "Francais" {
		t.Errorf("stored = %+v", site.cfg.Locales)
	}
}

func TestSaveLocalesRejectsHiddenDefault(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	cfg, _ := site.Get(context.Background())
	errs, err := g.a.saveLocales(context.Background(), cfg, []siteconfig.LocaleEntry{{Tag: "en", Hidden: true}, {Tag: "fr", Segment: "fr"}}, false, "")
	if err != nil || !strings.Contains(errs["locales"], "cannot be hidden") {
		t.Fatalf("errs=%v err=%v", errs, err)
	}
	if g.flushed != 0 || !site.cfg.Locales[1].Hidden {
		t.Errorf("a rejected save changed state: flushed=%d %+v", g.flushed, site.cfg.Locales)
	}
	// An x-default failure lands on its own field.
	g.a.deps.ValidateLocales = func(context.Context, string, []siteconfig.LocaleEntry, bool, string) ([]siteconfig.LocaleEntry, string, error) {
		return nil, "", fieldErr{"x_default_locale", "x-default locale is hidden"}
	}
	errs, _ = g.a.saveLocales(context.Background(), cfg, site.cfg.Locales, false, "fr")
	if errs["xdefault"] == "" || errs["locales"] != "" {
		t.Errorf("errs = %v", errs)
	}
}

func TestSaveLocalesEmptyListResets(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	site.cfg.PrefixAll, site.cfg.XDefaultLocale = true, "en"
	cfg, _ := site.Get(context.Background())
	if errs, err := g.a.saveLocales(context.Background(), cfg, nil, true, "en"); err != nil || len(errs) != 0 {
		t.Fatalf("errs=%v err=%v", errs, err)
	}
	if len(site.cfg.Locales) != 0 || site.cfg.PrefixAll || site.cfg.XDefaultLocale != "" {
		t.Errorf("config = %+v", site.cfg)
	}
}

func TestSettingsNavBadgeShowsLocaleCount(t *testing.T) {
	t.Parallel()
	g, _ := localeFixture(t)
	rec := g.do("GET", "/admin/settings/general", "owner-token", nil, nil)
	body := rec.Body.String()
	if !strings.Contains(body, `<span class="hm-subnav__marker">2</span>`) {
		t.Errorf("Locales link has no count badge:\n%s", body)
	}
	// A single-locale site shows no badge.
	g2 := newOwnerRig(t, true)
	body = g2.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String()
	if strings.Contains(body, "hm-subnav__marker") {
		t.Error("single-locale site shows a badge")
	}
}
