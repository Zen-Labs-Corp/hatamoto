package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
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
	errs, err := g.a.saveLocales(context.Background(), cfg, entries, false, "", nil)
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
	errs, err := g.a.saveLocales(context.Background(), cfg, []siteconfig.LocaleEntry{{Tag: "en", Hidden: true}, {Tag: "fr", Segment: "fr"}}, false, "", nil)
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
	errs, _ = g.a.saveLocales(context.Background(), cfg, site.cfg.Locales, false, "fr", nil)
	if errs["xdefault"] == "" || errs["locales"] != "" {
		t.Errorf("errs = %v", errs)
	}
}

func TestSaveLocalesEmptyListResets(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	site.cfg.PrefixAll, site.cfg.XDefaultLocale = true, "en"
	cfg, _ := site.Get(context.Background())
	if errs, err := g.a.saveLocales(context.Background(), cfg, nil, true, "en", nil); err != nil || len(errs) != 0 {
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
	if c := postForm(g, "/update/fr", "public", "true"); c != 303 {
		t.Fatalf("code = %d", c)
	}
	if l := site.cfg.Locales[1]; l.Hidden || l.Name != "Francais" || l.Segment != "fr" {
		t.Errorf("fr = %+v", l)
	}
	if c := postForm(g, "/update/en", "public", "false"); c != 303 || site.cfg.Locales[0].Hidden || g.flushed != 1 {
		t.Errorf("hiding the default: code=%d %+v flushed=%d", c, site.cfg.Locales[0], g.flushed)
	}
	if c := postForm(g, "/update/xx", "public", "true"); c != 404 {
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

func TestAddressesSitemapNaming(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	body := g.do("GET", "/admin/settings/locales/addresses", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `name="sitemap_naming"`) || !strings.Contains(body, `<option value="group" selected`) {
		t.Error("the select is missing or group is not the default")
	}
	if c := postForm(g, "/addresses", "sitemap_naming", "legacy"); c != 303 || site.cfg.SitemapNaming != "legacy" {
		t.Fatalf("code=%d naming=%q", c, site.cfg.SitemapNaming)
	}
	if c := postForm(g, "/addresses", "sitemap_naming", "x"); c != 422 || site.cfg.SitemapNaming != "legacy" {
		t.Errorf("bad value: code=%d naming=%q", c, site.cfg.SitemapNaming)
	}
	if c := postForm(g, "/addresses", "xdefault", ""); c != 303 || site.cfg.SitemapNaming != "legacy" {
		t.Errorf("omitted field changed naming: code=%d naming=%q", c, site.cfg.SitemapNaming)
	}
	// Locale list saves leave it alone.
	if c := postForm(g, "/update/fr", "public", "true"); c != 303 || site.cfg.SitemapNaming != "legacy" {
		t.Errorf("locale update: code=%d naming=%q", c, site.cfg.SitemapNaming)
	}
}

func TestAddressesRefusesLegacyForFileNameSegment(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	site.cfg.Locales = []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "index"}}
	rec := g.do("POST", "/admin/settings/locales/addresses", "owner-token", url.Values{"sitemap_naming": {"legacy"}}, nil)
	if rec.Code != 422 || site.cfg.SitemapNaming == "legacy" || !strings.Contains(rec.Body.String(), "sitemap-index.xml") {
		t.Errorf("code=%d naming=%q", rec.Code, site.cfg.SitemapNaming)
	}
}

func TestAddressesRejectsReservedSegmentField(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	g.a.deps.ValidateLocales = func(context.Context, string, []siteconfig.LocaleEntry, bool, string) ([]siteconfig.LocaleEntry, string, error) {
		return nil, "", fieldErr{"locales", `segment "misc" is a route of its own`}
	}
	rec := g.do("POST", "/admin/settings/locales/addresses", "owner-token", url.Values{"sitemap_naming": {"legacy"}}, nil)
	if rec.Code != 422 || site.cfg.SitemapNaming != "" {
		t.Errorf("code=%d naming=%q", rec.Code, site.cfg.SitemapNaming)
	}
}

func TestLocalePagesPassA11y(t *testing.T) {
	t.Parallel()
	g, _ := localeFixture(t)
	for _, p := range []string{"", "/new", "/edit/fr", "/addresses", "/business"} {
		rec := g.do("GET", "/admin/settings/locales"+p, "owner-token", nil, nil)
		if rec.Code != 200 {
			t.Errorf("%s = %d", p, rec.Code)
			continue
		}
		if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %v", p, issues)
		}
	}
}

func TestAddressesTabEmptyOnASingleLocaleSite(t *testing.T) {
	t.Parallel()
	g := newOwnerRig(t, true)
	site := g.a.deps.Site.(*fakeSite)
	body := g.do("GET", "/admin/settings/locales/addresses", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Add a second locale first") || !strings.Contains(body, `href="/admin/settings/locales/new"`) || strings.Contains(body, `name="prefix_all"`) || strings.Contains(body, `name="sitemap_naming"`) {
		t.Error("single-locale Addresses tab shows the form instead of the empty state")
	}
	rec := g.do("POST", "/admin/settings/locales/addresses", "owner-token", url.Values{"prefix_all": {"true"}}, nil)
	if rec.Code != 422 || site.cfg.PrefixAll || g.flushed != 0 {
		t.Errorf("code=%d prefixAll=%v flushed=%d: a single-locale save must be refused", rec.Code, site.cfg.PrefixAll, g.flushed)
	}
}

func TestLocaleRemove(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	site.cfg.Locales = append(site.cfg.Locales, siteconfig.LocaleEntry{Tag: "nl-NL", Segment: "nl-nl"})
	rec := g.do("GET", "/admin/settings/locales/remove/nl-NL", "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "stop being reachable") || !strings.Contains(rec.Body.String(), `action="/admin/settings/locales/remove/nl-NL"`) {
		t.Fatalf("confirm page: code=%d", rec.Code)
	}
	if len(site.cfg.Locales) != 3 {
		t.Fatal("the confirm page removed the locale")
	}
	if c := postForm(g, "/remove/nl-NL"); c != 303 {
		t.Fatalf("code = %d", c)
	}
	if l := site.cfg.Locales; len(l) != 2 || l[0].Tag != "en" || l[1].Tag != "fr" || !l[1].Hidden || g.flushed != 1 {
		t.Errorf("locales = %+v flushed=%d", l, g.flushed)
	}
	if c := postForm(g, "/remove/nl-NL"); c != 404 {
		t.Errorf("removing it again = %d, want 404", c)
	}
}

func TestLocaleRemoveRefusesDefaultAndXDefault(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	site.cfg.Locales[1].Hidden = false
	site.cfg.Locales = append(site.cfg.Locales, siteconfig.LocaleEntry{Tag: "nl-NL", Segment: "nl-nl"})
	site.cfg.XDefaultLocale = "nl-NL"
	for _, tag := range []string{"en", "nl-NL"} {
		for _, method := range []string{"GET", "POST"} {
			rec := g.do(method, "/admin/settings/locales/remove/"+tag, "owner-token", url.Values{}, nil)
			if rec.Code != 422 {
				t.Errorf("%s %s = %d, want 422", method, tag, rec.Code)
			}
		}
	}
	if len(site.cfg.Locales) != 3 || g.flushed != 0 {
		t.Errorf("a refused removal changed state: %+v flushed=%d", site.cfg.Locales, g.flushed)
	}
	body := g.do("POST", "/admin/settings/locales/remove/nl-NL", "owner-token", url.Values{}, nil).Body.String()
	if !strings.Contains(body, "default for search engines") {
		t.Error("the x-default refusal does not say why")
	}
}

func TestLocaleRemoveLastBackToSingleLocale(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	site.cfg.Locales[0].Segment = "en"
	site.cfg.PrefixAll, site.cfg.XDefaultLocale = true, "en"
	if c := postForm(g, "/remove/fr"); c != 303 {
		t.Fatalf("code = %d", c)
	}
	if len(site.cfg.Locales) != 0 || site.cfg.PrefixAll || site.cfg.XDefaultLocale != "" || g.flushed != 1 {
		t.Errorf("config = %+v flushed=%d", site.cfg, g.flushed)
	}
}

func TestLocaleRowMenuOffersRemoveOnlyOffTheDefault(t *testing.T) {
	t.Parallel()
	g, _ := localeFixture(t)
	body := g.do("GET", "/admin/settings/locales", "owner-token", nil, nil).Body.String()
	if strings.Count(body, "/admin/settings/locales/remove/") == 0 || strings.Contains(body, "/admin/settings/locales/remove/en") {
		t.Error("Remove is missing on fr or offered on the default locale")
	}
}

func TestLocaleHidingTheXDefaultIsReportedWhereTheOwnerActed(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	site.cfg.Locales[1].Hidden = false
	site.cfg.XDefaultLocale = "fr"
	g.a.deps.ValidateLocales = func(_ context.Context, _ string, e []siteconfig.LocaleEntry, _ bool, x string) ([]siteconfig.LocaleEntry, string, error) {
		for _, l := range e {
			if l.Hidden && l.Tag == x {
				return nil, "", fieldErr{"x_default_locale", "x-default locale is hidden"}
			}
		}
		return e, x, nil
	}
	// From the row menu: back to the list with the error as a flash.
	rec := g.do("POST", "/admin/settings/locales/update/fr", "owner-token", url.Values{"public": {"false"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/locales" || !strings.Contains(strings.Join(rec.Header().Values("Set-Cookie"), ";"), "flash") {
		t.Errorf("row action: code=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	// From the edit page: the message sits on the Public switch and the summary links to it.
	rec = g.do("POST", "/admin/settings/locales/update/fr", "owner-token", url.Values{"segment": {"fr"}, "name": {""}, "public": {"false"}}, nil)
	body := rec.Body.String()
	if rec.Code != 422 || strings.Count(body, "x-default locale is hidden") < 2 || !strings.Contains(body, `href="#locale-public"`) {
		t.Errorf("edit page: code=%d, message not on the Public switch", rec.Code)
	}
	if site.cfg.Locales[1].Hidden {
		t.Error("a refused save hid the locale")
	}
}

func TestLocaleAddShowsAReservedSegmentOnThePrefixField(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	g.a.deps.ValidateLocales = func(context.Context, string, []siteconfig.LocaleEntry, bool, string) ([]siteconfig.LocaleEntry, string, error) {
		return nil, "", fieldErr{"locales", "url segment admin is a route of its own"}
	}
	rec := g.do("POST", "/admin/settings/locales/add", "owner-token", url.Values{"tag": {"nl-NL"}, "segment": {"admin"}, "public": {"true"}}, nil)
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, `href="#locale-segment"`) || strings.Count(body, "a route of its own") < 2 || len(site.cfg.Locales) != 2 {
		t.Errorf("code=%d: the message is not on the Address prefix field", rec.Code)
	}
}

func TestLocalePrefixHelpSaysWhatEmptyDoes(t *testing.T) {
	t.Parallel()
	g, _ := localeFixture(t)
	get := func(p string) string {
		return g.do("GET", "/admin/settings/locales"+p, "owner-token", nil, nil).Body.String()
	}
	if b := get("/new"); !strings.Contains(b, "Empty uses the full code, like fr-ca") || strings.Contains(b, "Empty uses the language code") {
		t.Error("the add form misdescribes an empty prefix")
	}
	if b := get("/edit/en"); !strings.Contains(b, "Empty keeps this locale at the root of the site.") {
		t.Error("the default locale page does not say an empty prefix keeps the root")
	}
	if b := get("/edit/fr"); !strings.Contains(b, "Empty uses the full code.") || strings.Contains(b, "root of the site") {
		t.Error("another locale page misdescribes an empty prefix")
	}
}

func TestLocalesTableBadgesDefaultAndSearchEngineDefault(t *testing.T) {
	t.Parallel()
	g, site := localeFixture(t)
	site.cfg.Locales[1].Hidden = false
	row := func(body, tag string) string {
		_, after, _ := strings.Cut(body, `<tr`)
		for _, r := range strings.Split(after, `<tr`) {
			if strings.Contains(r, `<span class="hm-table__sub">`+tag+`</span>`) {
				return r
			}
		}
		return ""
	}
	site.cfg.XDefaultLocale = "fr"
	body := g.do("GET", "/admin/settings/locales", "owner-token", nil, nil).Body.String()
	if r := row(body, "en"); !strings.Contains(r, ">Default</span>") || strings.Contains(r, "search engine") {
		t.Errorf("default row: %s", r)
	}
	if r := row(body, "fr"); !strings.Contains(r, ">Search engine default</span>") || strings.Contains(r, ">Default</span>") {
		t.Errorf("x-default row: %s", r)
	}
	site.cfg.XDefaultLocale = "en"
	body = g.do("GET", "/admin/settings/locales", "owner-token", nil, nil).Body.String()
	if r := row(body, "en"); !strings.Contains(r, ">Default, also for search engines</span>") || strings.Contains(r, ">Default</span>") {
		t.Errorf("combined row: %s", r)
	}
}

func TestLocaleOrderFlashNamesTheLocaleAndDirection(t *testing.T) {
	t.Parallel()
	g, _ := localeFixture(t)
	f := url.Values{"order": {"fr,en"}, "moved": {"fr"}, "dir": {"up"}}
	rec := g.do("POST", "/admin/settings/locales/order", "owner-token", f, nil)
	if rec.Code != 303 {
		t.Fatalf("code = %d", rec.Code)
	}
	if body := followFlashTo(g, "/admin/settings/locales", rec); !strings.Contains(body, "Moved French up.") {
		t.Error("the flash does not name the locale and direction")
	}
	body := g.do("GET", "/admin/settings/locales", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `name="moved" value="fr"`) || !strings.Contains(body, `name="dir" value="down"`) {
		t.Error("the row menu does not tell the server which locale moved")
	}
}

func TestLocaleNameFieldDoesNotPromiseAPublicEffect(t *testing.T) {
	t.Parallel()
	g, _ := localeFixture(t)
	for _, p := range []string{"/new", "/edit/fr"} {
		body := g.do("GET", "/admin/settings/locales"+p, "owner-token", nil, nil).Body.String()
		if !strings.Contains(body, "Display name") || !strings.Contains(body, "Shown in the admin. Themes can use it later.") || strings.Contains(body, "shown to visitors") {
			t.Errorf("%s: the name field is not labelled as an admin display name", p)
		}
	}
}
