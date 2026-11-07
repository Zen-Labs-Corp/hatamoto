package admin

import (
	"encoding/json/jsontext"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/themestrings"
)

func newThemeStringsRig(t *testing.T, locales ...siteconfig.LocaleEntry) (*seoRig, *fakeSite) {
	t.Helper()
	s := newSEORig(t, newFakeSEO())
	site := newFakeSite("Acme Site")
	site.cfg.Language = "en"
	site.cfg.Locales = locales
	site.cfg.ThemeExtra = jsontext.Value(`{"socialLinks":{"github":"https://g.example"}}`)
	s.a.deps.Site = site
	s.a.deps.Growth.Packages = &fakePackages{
		enabled: map[string]bool{},
		themes:  []ThemeInfo{{Name: "default", Builtin: true}, {Name: "sunrise", Version: "1.0.0", Active: true}},
		themeStrings: themestrings.Decl{
			"search":     "Search",
			"pager_next": "Next",
		},
	}
	return s, site
}

var twoLocales = []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr", Hidden: true}}

func TestThemeStringsPageGolden(t *testing.T) {
	for _, c := range []struct {
		name    string
		locales []siteconfig.LocaleEntry
		tabs    bool
	}{{"settings-theme-strings", nil, false}, {"settings-theme-strings-multi", twoLocales, true}} {
		t.Run(c.name, func(t *testing.T) {
			s, _ := newThemeStringsRig(t, c.locales...)
			body := s.get(t, "/admin/theme/strings?locale=fr")
			if got := strings.Contains(body, `aria-label="Locale"`); got != c.tabs {
				t.Errorf("locale tabs shown = %v, want %v", got, c.tabs)
			}
			for _, want := range []string{"Leave blank to use the theme default", "pager next", `name="s_search"`} {
				if !strings.Contains(body, want) {
					t.Errorf("page lacks %q", want)
				}
			}
			compareGolden(t, "pages/"+c.name+".html", normalizePage(body))
		})
	}
}

func TestThemeStringsRowLabelsAreUnique(t *testing.T) {
	s, _ := newThemeStringsRig(t, twoLocales...)
	body := s.get(t, "/admin/theme/strings?locale=fr")
	for _, want := range []string{"French: search", "French: pager next"} {
		if strings.Count(body, want) != 1 {
			t.Errorf("label %q appears %d times, want 1", want, strings.Count(body, want))
		}
	}
}

func TestThemeStringsSaveStoresPrunesAndAudits(t *testing.T) {
	s, site := newThemeStringsRig(t, twoLocales...)
	site.cfg.ThemeExtra = jsontext.Value(`{"socialLinks":{"a":"b"},"themeStrings":{"other":{"fr":{"search":"keep"}},"sunrise":{"en":{"search":"Find"},"fr":{"pager_next":"old","gone":"orphan"}}}}`)
	rec := s.do("POST", "/admin/theme/strings", "owner-token", url.Values{
		"locale": {"fr"}, "s_search": {" Chercher "}, "s_pager_next": {"  "},
	}, nil)
	if rec.Code != 303 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body.String())
	}
	o := themestrings.ParseOverrides(site.cfg.ThemeExtra)
	if got := o.For("sunrise", "fr"); len(got) != 2 || got["search"] != "Chercher" || got["gone"] != "orphan" {
		t.Errorf("fr = %v, want search set, pager_next pruned, orphan kept", got)
	}
	if o.For("sunrise", "en")["search"] != "Find" || o.For("other", "fr")["search"] != "keep" {
		t.Errorf("other locale or theme touched: %s", site.cfg.ThemeExtra)
	}
	if !strings.Contains(string(site.cfg.ThemeExtra), `"socialLinks"`) {
		t.Error("another theme_extra key was lost")
	}
	if s.flushed != 1 {
		t.Errorf("page cache cleared %d times, want 1", s.flushed)
	}
	if len(s.events) != 1 || s.events[0].Action != "theme.strings" {
		t.Fatalf("audit = %+v", s.events)
	}
	if j := toJSON(t, s.events[0]); strings.Contains(j, "Chercher") || !strings.Contains(j, `"set":1`) || !strings.Contains(j, `"cleared":1`) {
		t.Errorf("audit detail = %s", j)
	}
}

func TestThemeStringsSaveEmptyRemovesTheTree(t *testing.T) {
	s, site := newThemeStringsRig(t)
	site.cfg.ThemeExtra = jsontext.Value(`{"themeStrings":{"sunrise":{"en":{"search":"Find"}}}}`)
	rec := s.do("POST", "/admin/theme/strings", "owner-token", url.Values{"locale": {"en"}, "s_search": {""}}, nil)
	if rec.Code != 303 {
		t.Fatalf("save = %d", rec.Code)
	}
	if got := themestrings.ParseOverrides(site.cfg.ThemeExtra); len(got) != 0 {
		t.Errorf("stored = %v, want nothing", got)
	}
}

func TestThemeStringsSaveRefusesBadPosts(t *testing.T) {
	for name, form := range map[string]url.Values{
		"unknown key":  {"locale": {"en"}, "s_nope": {"x"}},
		"unknown tag":  {"locale": {"xx"}, "s_search": {"x"}},
		"missing tag":  {"s_search": {"x"}},
		"markup":       {"locale": {"en"}, "s_search": {"<b>x</b>"}},
		"over length":  {"locale": {"en"}, "s_search": {strings.Repeat("a", 301)}},
		"control char": {"locale": {"en"}, "s_search": {"a\x01b"}},
	} {
		t.Run(name, func(t *testing.T) {
			s, site := newThemeStringsRig(t, twoLocales...)
			before := string(site.cfg.ThemeExtra)
			rec := s.do("POST", "/admin/theme/strings", "owner-token", form, nil)
			if rec.Code != 422 {
				t.Fatalf("status %d, want 422", rec.Code)
			}
			if string(site.cfg.ThemeExtra) != before || s.flushed != 0 || len(s.events) != 0 {
				t.Error("a refused post saved, audited or cleared the cache")
			}
			if !strings.Contains(rec.Body.String(), `class="hm-formerrors`) {
				t.Error("no error summary in the page")
			}
		})
	}
}

func TestThemeStringsOwnerOnlyAndUnavailable(t *testing.T) {
	s, _ := newThemeStringsRig(t)
	if rec := s.do("GET", "/admin/theme/strings", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d, want 403", rec.Code)
	}
	if rec := s.do("POST", "/admin/theme/strings", "editor-token", url.Values{"locale": {"en"}}, nil); rec.Code != 403 {
		t.Errorf("editor POST = %d, want 403", rec.Code)
	}
	s.a.deps.Growth.Packages = nil
	if rec := s.do("GET", "/admin/theme/strings", "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("no packages: status %d, want 503", rec.Code)
	}
}

func TestThemeStringsErrorGolden(t *testing.T) {
	s, _ := newThemeStringsRig(t)
	rec := s.do("POST", "/admin/theme/strings", "owner-token", url.Values{"locale": {"en"}, "s_search": {"<b>"}, "s_pager_next": {"Onward"}}, nil)
	if rec.Code != 422 {
		t.Fatalf("status %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `value="Onward"`) {
		t.Error("the text that was valid is not kept in the form")
	}
	compareGolden(t, "pages/settings-theme-strings-error.html", normalizePage(body))
}
