package admin

import (
	"context"
	"net/url"
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

func postForm(g *ownerRig, path string, kv ...string) int {
	f := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		f.Set(kv[i], kv[i+1])
	}
	return g.do("POST", "/admin/settings/locales"+path, "owner-token", f, nil).Code
}

func TestLocalesPageListsLocalesAndHidden(t *testing.T) {
	t.Parallel()
	g, _ := localeFixture(t)
	rec := g.do("GET", "/admin/settings/locales", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Francais") || !strings.Contains(body, "Hidden, not public yet") || !strings.Contains(body, "Default") {
		t.Fatalf("code=%d body missing locale rows", rec.Code)
	}
}

func TestLocaleAddHidden(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	if c := postForm(g, "/add", "tag", "nl-NL", "public", "false"); c != 303 {
		t.Fatalf("code = %d", c)
	}
	l := site.cfg.Locales
	if len(l) != 3 || l[2].Tag != "nl-NL" || !l[2].Hidden || l[2].Segment != "nl-nl" || g.flushed != 1 {
		t.Errorf("locales = %+v flushed=%d", l, g.flushed)
	}
}

func TestLocaleAddRejectsUndAndDuplicates(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	for _, tag := range []string{"und", "fr", "not a tag"} {
		if c := postForm(g, "/add", "tag", tag, "public", "true"); c != 422 {
			t.Errorf("tag %q code = %d, want 422", tag, c)
		}
	}
	if len(site.cfg.Locales) != 2 {
		t.Errorf("locales = %+v", site.cfg.Locales)
	}
}

func TestLocaleOrderPermutationOnly(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	for _, o := range []string{"en", "en,en", "en,fr,es", "fr,xx"} {
		if c := postForm(g, "/order", "order", o); c != 422 {
			t.Errorf("order %q code = %d, want 422", o, c)
		}
	}
	if c := postForm(g, "/order", "order", "fr,en"); c != 303 || site.cfg.Locales[0].Tag != "fr" || site.cfg.Locales[0].Name != "Francais" {
		t.Errorf("code=%d locales=%+v", c, site.cfg.Locales)
	}
}

func TestLocaleUpdateKeepsOthers(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	if c := postForm(g, "/fr/update", "public", "true"); c != 303 {
		t.Fatalf("code = %d", c)
	}
	if l := site.cfg.Locales[1]; l.Hidden || l.Name != "Francais" || l.Segment != "fr" {
		t.Errorf("fr = %+v", l)
	}
	if c := postForm(g, "/en/update", "public", "false"); c != 422 || site.cfg.Locales[0].Hidden {
		t.Errorf("hiding the default: code=%d %+v", c, site.cfg.Locales[0])
	}
	if c := postForm(g, "/xx/update", "public", "true"); c != 404 {
		t.Errorf("unknown tag code = %d", c)
	}
}

func TestLocalesPageSingleLocaleOnlyAdd(t *testing.T) {
	t.Parallel()
	g := newOwnerRig(t, true)
	rec := g.do("GET", "/admin/settings/locales", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Add locale") || strings.Contains(body, "Move down") || strings.Contains(body, "Hide from visitors") {
		t.Fatalf("code=%d single-locale page shows multi controls", rec.Code)
	}
}

func TestAddressesSavePreservesHiddenAndName(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	if c := postForm(g, "/addresses", "prefix_all", "true", "xdefault", ""); c != 303 {
		t.Fatalf("code = %d", c)
	}
	if l := site.cfg.Locales[1]; !l.Hidden || l.Name != "Francais" || !site.cfg.PrefixAll || g.flushed != 1 {
		t.Errorf("fr = %+v prefixAll=%v flushed=%d", l, site.cfg.PrefixAll, g.flushed)
	}
}

func TestAddressesRejectsHiddenXDefault(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	g.a.deps.ValidateLocales = func(context.Context, string, []siteconfig.LocaleEntry, bool, string) ([]siteconfig.LocaleEntry, string, error) {
		return nil, "", fieldErr{"x_default_locale", "x-default locale is hidden"}
	}
	f := url.Values{"xdefault": {"fr"}}
	rec := g.do("POST", "/admin/settings/locales/addresses", "owner-token", f, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "x-default locale is hidden") || site.cfg.XDefaultLocale != "" {
		t.Errorf("code=%d xdefault=%q", rec.Code, site.cfg.XDefaultLocale)
	}
	body := g.do("GET", "/admin/settings/locales/addresses", "owner-token", nil, nil).Body.String()
	if strings.Contains(body, `value="fr"`) {
		t.Error("a hidden locale is offered as x-default")
	}
}

func TestAddressesPrefixAll(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	site.cfg.PrefixAll = true
	body := g.do("GET", "/admin/settings/locales/addresses", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "checked") {
		t.Error("prefix_all switch is not checked")
	}
	if c := postForm(g, "/addresses", "prefix_all", "false"); c != 303 || site.cfg.PrefixAll {
		t.Errorf("code=%d prefixAll=%v", c, site.cfg.PrefixAll)
	}
}
