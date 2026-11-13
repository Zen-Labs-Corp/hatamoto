package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"context"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/siteconfig"
)

func localeEntrySet(t *testing.T, prefixAll bool) *entity.LocaleSet {
	t.Helper()
	enSegment := ""
	if prefixAll {
		enSegment = "en"
	}
	set, err := entity.NewLocaleSet("en", []entity.LocaleEntry{
		{Tag: "en", Segment: enSegment},
		{Tag: "fr", Segment: "fr", Countries: []string{"FR", "BE"}},
		{Tag: "es", Segment: "es", Hidden: true, Countries: []string{"ES"}},
	}, prefixAll, "")
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// entryRig runs the wrapper over a recording next handler.
type entryRig struct {
	h      http.Handler
	served int
}

func newEntryRig(t *testing.T, set *entity.LocaleSet, entry siteconfig.VisitorEntry, ok bool) *entryRig {
	t.Helper()
	rig := &entryRig{}
	s := &Server{cfg: &config.Config{}}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rig.served++
		w.WriteHeader(http.StatusOK)
	})
	rig.h = s.localeEntryFrom(next, func(context.Context) (localeEntryState, bool) {
		return localeEntryState{set: set, entry: entry}, ok
	})
	return rig
}

type entryReq struct {
	method, target string
	headers        map[string]string
	cookies        map[string]string
}

func (rig *entryRig) do(q entryReq) *httptest.ResponseRecorder {
	if q.method == "" {
		q.method = http.MethodGet
	}
	req := httptest.NewRequest(q.method, q.target, nil)
	for k, v := range q.headers {
		req.Header.Set(k, v)
	}
	for k, v := range q.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	rec := httptest.NewRecorder()
	rig.h.ServeHTTP(rec, req)
	return rec
}

func TestLocaleEntryRedirect(t *testing.T) {
	on := siteconfig.VisitorEntry{Enabled: true, Paths: []string{"/contact"}, LegacyCookie: "old_loc"}
	cases := []struct {
		name      string
		prefixAll bool
		entry     siteconfig.VisitorEntry
		req       entryReq
		want      string // Location; "" = passed to next
	}{
		{"country picks the locale", false, on, entryReq{target: "/", headers: map[string]string{"CF-IPCountry": "FR"}}, "/fr"},
		{"lower-case CloudFront header", false, on, entryReq{target: "/", headers: map[string]string{"CloudFront-Viewer-Country": "be"}}, "/fr"},
		{"cookie beats country", false, on, entryReq{target: "/", headers: map[string]string{"CF-IPCountry": "FR"}, cookies: map[string]string{"hm_locale": "en"}}, ""},
		{"cookie to a non-default beats country", false, on, entryReq{target: "/", cookies: map[string]string{"hm_locale": "fr"}}, "/fr"},
		{"hidden locale country is ignored", false, on, entryReq{target: "/", headers: map[string]string{"CF-IPCountry": "ES"}}, ""},
		{"hidden cookie value is ignored, country decides", false, on, entryReq{target: "/", headers: map[string]string{"CF-IPCountry": "FR"}, cookies: map[string]string{"hm_locale": "es"}}, "/fr"},
		{"unknown cookie value is ignored", false, on, entryReq{target: "/", headers: map[string]string{"CF-IPCountry": "FR"}, cookies: map[string]string{"hm_locale": "zz"}}, "/fr"},
		{"unknown country falls to the default", false, on, entryReq{target: "/", headers: map[string]string{"CF-IPCountry": "ZZ"}}, ""},
		{"malformed country is ignored", false, on, entryReq{target: "/", headers: map[string]string{"CF-IPCountry": "FRA"}}, ""},
		{"legacy cookie when the core cookie is absent", false, on, entryReq{target: "/", cookies: map[string]string{"old_loc": "fr"}}, "/fr"},
		{"legacy cookie loses to the core cookie", false, on, entryReq{target: "/", cookies: map[string]string{"hm_locale": "en", "old_loc": "fr"}}, ""},
		{"legacy cookie is not read when the core cookie is invalid", false, on, entryReq{target: "/", cookies: map[string]string{"hm_locale": "zz", "old_loc": "fr"}}, ""},
		{"legacy cookie with a hidden segment", false, on, entryReq{target: "/", cookies: map[string]string{"old_loc": "es"}}, ""},
		{"legacy cookie with a bad value", false, on, entryReq{target: "/", cookies: map[string]string{"old_loc": "../x"}}, ""},
		{"legacy cookie unset in settings", false, siteconfig.VisitorEntry{Enabled: true}, entryReq{target: "/", cookies: map[string]string{"old_loc": "fr"}}, ""},
		{"query is kept", false, on, entryReq{target: "/?utm=a&b=%2F", headers: map[string]string{"CF-IPCountry": "FR"}}, "/fr?utm=a&b=%2F"},
		{"entry path keeps path and query", false, on, entryReq{target: "/contact?x=1", cookies: map[string]string{"hm_locale": "fr"}}, "/fr/contact?x=1"},
		{"entry path with trailing slash", false, on, entryReq{target: "/contact/", cookies: map[string]string{"hm_locale": "fr"}}, "/fr/contact"},
		{"path that is not an entry path", false, on, entryReq{target: "/about", cookies: map[string]string{"hm_locale": "fr"}}, ""},
		{"HEAD like GET", false, on, entryReq{method: http.MethodHead, target: "/", headers: map[string]string{"CF-IPCountry": "FR"}}, "/fr"},
		{"POST untouched", false, on, entryReq{method: http.MethodPost, target: "/", headers: map[string]string{"CF-IPCountry": "FR"}}, ""},
		{"setting off", false, siteconfig.VisitorEntry{}, entryReq{target: "/", headers: map[string]string{"CF-IPCountry": "FR"}}, ""},
		{"prefix-all default pick redirects", true, on, entryReq{target: "/"}, "/en"},
		{"prefix-all entry path", true, on, entryReq{target: "/contact"}, "/en/contact"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rig := newEntryRig(t, localeEntrySet(t, c.prefixAll), c.entry, true)
			rec := rig.do(c.req)
			if c.want == "" {
				if rig.served != 1 || rec.Code != http.StatusOK || rec.Header().Get("Location") != "" {
					t.Fatalf("served %d, status %d, Location %q; want next", rig.served, rec.Code, rec.Header().Get("Location"))
				}
				return
			}
			if rig.served != 0 || rec.Code != http.StatusFound {
				t.Fatalf("served %d, status %d; want a 302", rig.served, rec.Code)
			}
			if got := rec.Header().Get("Location"); got != c.want {
				t.Errorf("Location %q, want %q", got, c.want)
			}
			if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
				t.Errorf("Cache-Control %q", got)
			}
			if got := rec.Header().Get("Vary"); got != "Cookie, CF-IPCountry, CloudFront-Viewer-Country" {
				t.Errorf("Vary %q", got)
			}
		})
	}
}

// A default-locale visitor on an unprefixed "/" is served the page as today,
// with the Vary tokens and no no-store.
func TestLocaleEntryDefaultPickFallsThroughWithVary(t *testing.T) {
	rig := newEntryRig(t, localeEntrySet(t, false), siteconfig.VisitorEntry{Enabled: true}, true)
	rec := rig.do(entryReq{target: "/"})
	if rig.served != 1 || rec.Code != http.StatusOK {
		t.Fatalf("served %d, status %d", rig.served, rec.Code)
	}
	if got := rec.Header().Get("Vary"); got != "Cookie, CF-IPCountry, CloudFront-Viewer-Country" {
		t.Errorf("Vary %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "" {
		t.Errorf("Cache-Control %q, want it left to the page", got)
	}
}

// With the setting off, on a single-locale site or when the config cannot be
// read, the request reaches next with no header added.
func TestLocaleEntryOffAddsNothing(t *testing.T) {
	single, err := entity.NewLocaleSet("en", nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]*entryRig{
		"setting off":   newEntryRig(t, localeEntrySet(t, false), siteconfig.VisitorEntry{}, true),
		"single locale": newEntryRig(t, single, siteconfig.VisitorEntry{Enabled: true}, true),
		"config error":  newEntryRig(t, localeEntrySet(t, false), siteconfig.VisitorEntry{Enabled: true}, false),
	}
	for name, rig := range cases {
		rec := rig.do(entryReq{target: "/", headers: map[string]string{"CF-IPCountry": "FR"}})
		if rig.served != 1 || rec.Code != http.StatusOK || len(rec.Header()) != 0 {
			t.Errorf("%s: served %d, status %d, headers %v", name, rig.served, rec.Code, rec.Header())
		}
	}
}

// /locale is the switcher on a multi-locale site whatever the setting says.
func TestLocaleEntryDispatchesTheSwitcher(t *testing.T) {
	rig := newEntryRig(t, localeEntrySet(t, false), siteconfig.VisitorEntry{}, true)
	rec := rig.do(entryReq{target: "/locale?l=fr&to=/fr/about"})
	if rig.served != 0 || rec.Code != http.StatusFound || rec.Header().Get("Location") != "/fr/about" {
		t.Fatalf("served %d, status %d, Location %q", rig.served, rec.Code, rec.Header().Get("Location"))
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "hm_locale=fr") {
		t.Errorf("Set-Cookie %q", rec.Header().Get("Set-Cookie"))
	}
	rig = newEntryRig(t, localeEntrySet(t, false), siteconfig.VisitorEntry{}, false)
	rig.do(entryReq{target: "/locale?l=fr"})
	if rig.served != 1 {
		t.Errorf("single-locale site: /locale served %d times by next, want 1", rig.served)
	}
}

// A locale that kept the segment "locale" owns /locale: the switcher stays off
// and the request reaches the mux, public or hidden locale alike.
func TestLocaleEntryLeavesALocaleSegmentLocaleAlone(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		set, err := entity.NewLocaleSet("en", []entity.LocaleEntry{
			{Tag: "en"},
			{Tag: "fr", Segment: "locale", Hidden: hidden},
		}, false, "")
		if err != nil {
			t.Fatal(err)
		}
		rig := newEntryRig(t, set, siteconfig.VisitorEntry{}, true)
		rec := rig.do(entryReq{target: "/locale"})
		if rig.served != 1 || rec.Code != http.StatusOK || rec.Header().Get("Set-Cookie") != "" {
			t.Errorf("hidden=%v: served %d, status %d, cookie %q", hidden, rig.served, rec.Code, rec.Header().Get("Set-Cookie"))
		}
	}
}
