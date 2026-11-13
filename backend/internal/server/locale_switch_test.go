package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/entity"
)

func localeSwitchSet(t *testing.T, prefixAll bool) *entity.LocaleSet {
	t.Helper()
	set, err := entity.NewLocaleSet("en", []entity.LocaleEntry{
		{Tag: "en"},
		{Tag: "fr", Segment: "fr"},
		{Tag: "es", Segment: "es", Hidden: true},
	}, prefixAll, "")
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestCleanLocalTarget(t *testing.T) {
	cases := []struct {
		name, to string
		want     bool
	}{
		{"home", "/", true},
		{"path", "/fr/about", true},
		{"empty", "", false},
		{"relative", "fr/x", false},
		{"double slash", "//evil.example", false},
		{"inner double slash", "/fr//x", false},
		{"backslash", "/fr\\x", false},
		{"enc slash upper", "/fr%2Fx", false},
		{"enc slash lower", "/fr%2fx", false},
		{"enc backslash upper", "/fr%5Cx", false},
		{"enc backslash lower", "/fr%5cx", false},
		{"enc dot upper", "/fr/%2E%2E/x", false},
		{"enc dot lower", "/fr/%2e/x", false},
		{"dot", "/fr/./x", false},
		{"dotdot", "/fr/../x", false},
		{"query", "/fr/x?a=1", false},
		{"fragment", "/fr/x#a", false},
		{"control", "/fr/x\r\nSet-Cookie: a=b", false},
		{"space", "/fr/a b", false},
		{"del", "/fr/x\x7f", false},
		{"too long", "/" + strings.Repeat("a", 2048), false},
		{"max length", "/" + strings.Repeat("a", 2047), true},
	}
	for _, c := range cases {
		if got := cleanLocalTarget(c.to); got != c.want {
			t.Errorf("%s: cleanLocalTarget(%q) = %v, want %v", c.name, c.to, got, c.want)
		}
	}
}

func TestLocaleSwitchRedirect(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	set := localeSwitchSet(t, false)
	cases := []struct {
		name, query, want string
	}{
		{"good path", "l=fr&to=/fr/about", "/fr/about"},
		{"locale root", "l=fr&to=/fr", "/fr"},
		{"no target", "l=fr", "/fr"},
		{"other locale", "l=fr&to=/es/x", "/fr"},
		{"sibling prefix", "l=fr&to=/frx", "/fr"},
		{"default home", "l=en&to=/about", "/about"},
		{"default into other locale", "l=en&to=/fr/about", "/"},
		{"scheme host", "l=fr&to=//evil.example", "/fr"},
		{"backslash", "l=fr&to=/fr/%5Cx", "/fr"},
		{"raw enc slash", "l=fr&to=%2Ffr%2Fx", "/fr"},
		{"raw enc slash lower", "l=fr&to=/fr%2fx", "/fr"},
		{"double encoded", "l=fr&to=/fr%252fx", "/fr"},
		{"dotdot", "l=fr&to=/fr/../x", "/fr"},
		{"query in target", "l=fr&to=" + url.QueryEscape("/fr/x?a=1"), "/fr"},
		{"fragment in target", "l=fr&to=" + url.QueryEscape("/fr/x#a"), "/fr"},
		{"control byte", "l=fr&to=/fr/x%0d%0aX:1", "/fr"},
		{"del byte", "l=fr&to=/fr/x%7f", "/fr"},
		{"too long", "l=fr&to=/fr/" + strings.Repeat("a", 2048), "/fr"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		s.localeSwitch(rec, httptest.NewRequest(http.MethodGet, "/locale?"+c.query, nil), set)
		if rec.Code != http.StatusFound {
			t.Errorf("%s: status %d, want 302", c.name, rec.Code)
			continue
		}
		if got := rec.Header().Get("Location"); got != c.want {
			t.Errorf("%s: Location %q, want %q", c.name, got, c.want)
		}
		if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
			t.Errorf("%s: Cache-Control %q", c.name, got)
		}
		if got := rec.Header().Values("Vary"); len(got) != 1 || got[0] != "Cookie" {
			t.Errorf("%s: Vary %q", c.name, got)
		}
	}
}

func TestLocaleSwitchCookie(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	set := localeSwitchSet(t, false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://example.test/locale?l=fr&to=/fr/about", nil)
	s.localeSwitch(rec, req, set)
	cs := rec.Result().Cookies()
	if len(cs) != 1 {
		t.Fatalf("cookies = %v, want one", cs)
	}
	c := cs[0]
	if c.Name != "hm_locale" || c.Value != "fr" || c.Path != "/" || c.MaxAge != 365*24*3600 ||
		!c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie = %+v", c)
	}
	if rec.Code != http.StatusFound {
		t.Errorf("status %d", rec.Code)
	}

	// Plain HTTP without a secure setting: not Secure; the operator flag forces it.
	rec = httptest.NewRecorder()
	s.localeSwitch(rec, httptest.NewRequest(http.MethodGet, "/locale?l=fr", nil), set)
	if rec.Result().Cookies()[0].Secure {
		t.Error("cookie Secure over plain HTTP without the flag")
	}
	s.cfg.AdminCookieSecure = true
	rec = httptest.NewRecorder()
	s.localeSwitch(rec, httptest.NewRequest(http.MethodGet, "/locale?l=fr", nil), set)
	if !rec.Result().Cookies()[0].Secure {
		t.Error("cookie not Secure with AdminCookieSecure")
	}

	// Unprefixed default: the cookie value is its tag.
	rec = httptest.NewRecorder()
	s.localeSwitch(rec, httptest.NewRequest(http.MethodGet, "/locale?l=en", nil), set)
	if c := rec.Result().Cookies()[0]; c.Value != "en" || rec.Header().Get("Location") != "/" {
		t.Errorf("default: cookie %+v Location %q", c, rec.Header().Get("Location"))
	}
}

func TestLocaleSwitchUnknownIs404(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	set := localeSwitchSet(t, false)
	for _, q := range []string{"l=es", "l=xx", "l=", "", "l=fr-FR", "l=es&to=/es/x"} {
		rec := httptest.NewRecorder()
		s.localeSwitch(rec, httptest.NewRequest(http.MethodGet, "/locale?"+q, nil), set)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%q: status %d, want 404", q, rec.Code)
		}
		if len(rec.Result().Cookies()) != 0 || rec.Header().Get("Location") != "" {
			t.Errorf("%q: cookie or Location set on 404", q)
		}
	}
}

func TestLocaleSwitchPrefixAllDefault(t *testing.T) {
	set, err := entity.NewLocaleSet("en", []entity.LocaleEntry{{Tag: "en", Segment: "en"}, {Tag: "fr", Segment: "fr"}}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: &config.Config{}}
	rec := httptest.NewRecorder()
	s.localeSwitch(rec, httptest.NewRequest(http.MethodGet, "/locale?l=en&to=/en/a", nil), set)
	if got := rec.Header().Get("Location"); got != "/en/a" {
		t.Errorf("Location %q", got)
	}
}
