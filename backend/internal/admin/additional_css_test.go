package admin

import (
	"html/template"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/render"
)

func newAdditionalCSSRig(t *testing.T) (*seoRig, *fakeSite) {
	t.Helper()
	s := newSEORig(t, newFakeSEO())
	site := newFakeSite("Acme Site")
	s.a.deps.Site = site
	return s, site
}

func TestAdditionalCSSPageGolden(t *testing.T) {
	s, site := newAdditionalCSSRig(t)
	site.cfg.ThemeExtra = []byte(`{"additional_css":"a{color:red}"}`)
	body := s.get(t, "/admin/theme/additional-css")
	for _, want := range []string{`name="additional_css"`, "a{color:red}", "0.0 KB of 64 KB", `<label class="hm-field__label" for="additional-css">`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	compareGolden(t, "pages/settings-additional-css.html", normalizePage(body))
}

func TestAdditionalCSSSave(t *testing.T) {
	s, site := newAdditionalCSSRig(t)
	css := "@media (width < 600px) {\r\n  a > b { color: red; }\r\n}\r\n"
	rec := s.do("POST", "/admin/theme/additional-css", "owner-token", url.Values{"additional_css": {css}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/theme/additional-css" {
		t.Fatalf("save = %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	want := strings.ReplaceAll(css, "\r\n", "\n")
	if got := render.AdditionalCSS(site.cfg.ThemeExtra); got != want {
		t.Errorf("stored %q, want %q", got, want)
	}
	if s.flushed != 1 {
		t.Errorf("page cache purged %d times, want 1", s.flushed)
	}
	if len(s.events) != 1 || s.events[0].Action != "theme.additional_css" || s.events[0].Detail["bytes"] != len(want) {
		t.Fatalf("audit = %+v", s.events)
	}
	// Empty text clears it and is audited with 0 bytes.
	rec = s.do("POST", "/admin/theme/additional-css", "owner-token", url.Values{"additional_css": {" \n"}}, nil)
	if rec.Code != 303 || render.AdditionalCSS(site.cfg.ThemeExtra) != "" || len(s.events) != 2 || s.events[1].Detail["bytes"] != 0 {
		t.Errorf("clear = %d, stored %q, audit %+v", rec.Code, render.AdditionalCSS(site.cfg.ThemeExtra), s.events)
	}
}

func TestAdditionalCSSRefuses(t *testing.T) {
	for _, tc := range []struct{ name, css, msg string }{
		{"import", "a{}\n@import url(x.css);", "line 2: @import is not allowed"},
		{"expression", "a{width:expression(1)}", "expression() is not allowed"},
		{"javascript", "a{background:url(javascript:x)}", "javascript: URLs are not allowed"},
		{"behavior", "a{behavior:url(x.htc)}", "behavior is not allowed"},
		{"binding", "a{-moz-binding:url(x)}", "-moz-binding is not allowed"},
		{"escape", `a{background:url('\6a avascript:x')}`, "javascript: URLs are not allowed"},
		{"too big", "/*" + strings.Repeat("x", render.MaxCustomCSS) + "*/", "over the 64 KB limit"},
	} {
		s, site := newAdditionalCSSRig(t)
		rec := s.do("POST", "/admin/theme/additional-css", "owner-token", url.Values{"additional_css": {tc.css}}, nil)
		body := rec.Body.String()
		if rec.Code != 422 || !strings.Contains(body, tc.msg) || !strings.Contains(body, `aria-invalid="true"`) || !strings.Contains(body, `href="#additional-css"`) {
			t.Errorf("%s: %d, want 422 with %q and the summary link", tc.name, rec.Code, tc.msg)
			continue
		}
		if tc.name != "too big" && !strings.Contains(body, template.HTMLEscapeString(tc.css)) {
			t.Errorf("%s: the typed text was not kept", tc.name)
		}
		if render.AdditionalCSS(site.cfg.ThemeExtra) != "" || s.flushed != 0 || len(s.events) != 0 {
			t.Errorf("%s: a refused save reached the store", tc.name)
		}
	}
	s, _ := newAdditionalCSSRig(t)
	rec := s.do("POST", "/admin/theme/additional-css", "owner-token", url.Values{"additional_css": {"a{}\n@import 'x';"}}, nil)
	compareGolden(t, "pages/settings-additional-css-error.html", normalizePage(rec.Body.String()))
}

func TestAdditionalCSSOwnerOnly(t *testing.T) {
	s, _ := newAdditionalCSSRig(t)
	for _, m := range []string{"GET", "POST"} {
		if rec := s.do(m, "/admin/theme/additional-css", "editor-token", url.Values{}, nil); rec.Code != 403 {
			t.Errorf("editor %s = %d, want 403", m, rec.Code)
		}
	}
}

// The CSS lands in a theme setting that is rendered into HTML, so the stored
// JSON must escape <, > and & like the v1 encoder did.
func TestAdditionalCSSStoredBytesAreHTMLSafe(t *testing.T) {
	s, site := newAdditionalCSSRig(t)
	css := "a::after{content:\"</style>&\"}"
	rec := s.do("POST", "/admin/theme/additional-css", "owner-token", url.Values{"additional_css": {css}}, nil)
	if rec.Code != 303 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body)
	}
	const want = `{"additional_css":"a::after{content:\"\u003c/style\u003e\u0026\"}"}`
	if got := string(site.cfg.ThemeExtra); got != want {
		t.Errorf("stored %s, want %s", got, want)
	}
}
