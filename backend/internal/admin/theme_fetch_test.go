package admin

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

func hasFlash(rec *httptest.ResponseRecorder) bool {
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == flashCookie {
			return true
		}
	}
	return false
}

var fetchHdr = map[string]string{"X-Hm-Fetch": "1"}

// The builder's Site styles drawer posts the two theme forms with X-Hm-Fetch: same validation, no page, no flash.
func TestThemeColoursFetchMode(t *testing.T) {
	t.Parallel()

	_, pk, fp := newPackageRig(t)
	site := newFakeSite("Acme Site")
	g := newRig(t, func(d *Deps) { d.Site = themeSite{site}; d.Growth.Plugins, d.Growth.Packages = fp, pk })
	form := url.Values{"primary": {"#112233"}, "secondary": {"#445566"}, "accent": {"#778899"}, "background": {"#ffffff"}, "text": {"#000000"},
		"font": {"Inter, sans-serif"}, "heading_font": {"Georgia, serif"}, "radius_sm": {"2px"}, "radius_md": {"6px"}, "radius_lg": {"1rem"}}

	rec := g.do("POST", "/admin/theme/colours", "owner-token", form, fetchHdr)
	if rec.Code != 204 || hasFlash(rec) || rec.Body.Len() != 0 {
		t.Fatalf("valid fetch save: %d flash=%v body=%q", rec.Code, hasFlash(rec), rec.Body.String())
	}
	if site.cfg.PrimaryColor != "#112233" {
		t.Errorf("primary colour = %q", site.cfg.PrimaryColor)
	}

	bad := url.Values{}
	for k, v := range form {
		bad[k] = v
	}
	bad.Set("primary", "red")
	bad.Set("radius_md", "huge")
	rec = g.do("POST", "/admin/theme/colours", "owner-token", bad, fetchHdr)
	var got struct{ Errors map[string]string }
	if rec.Code != 422 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Errors["primary"] == "" || got.Errors["radius_md"] == "" || len(got.Errors) != 2 {
		t.Fatalf("invalid fetch save: %d %s", rec.Code, rec.Body.String())
	}

	// without the header nothing changes: 303 and a flash
	rec = g.do("POST", "/admin/theme/colours", "owner-token", form, nil)
	if rec.Code != 303 || !hasFlash(rec) {
		t.Errorf("html save: %d flash=%v", rec.Code, hasFlash(rec))
	}
	if rec := g.do("POST", "/admin/theme/colours", "editor-token", form, fetchHdr); rec.Code != 403 {
		t.Errorf("editor save: %d, want 403", rec.Code)
	}
}

func TestThemeTokensFetchMode(t *testing.T) {
	t.Parallel()

	g, site, flushed := tokensRig(t, true)
	form := url.Values{"color_accent": {"#112233"}, "color_surface": {"#ffffff"}, "space_4": {"1.25rem"}}
	rec := g.do("POST", "/admin/theme/design-system", "owner-token", form, fetchHdr)
	if rec.Code != 204 || hasFlash(rec) || *flushed != 1 {
		t.Fatalf("valid fetch save: %d flash=%v purged=%d", rec.Code, hasFlash(rec), *flushed)
	}
	if d, _ := builder.ParseDesign(site.cfg.ThemeExtra); d.Tokens.Color["accent"] != "#112233" || d.Tokens.Space["4"] != "1.25rem" {
		t.Errorf("tokens: %+v", d.Tokens)
	}
	before := string(site.cfg.ThemeExtra)
	rec = g.do("POST", "/admin/theme/design-system", "owner-token", url.Values{"color_accent": {"nope"}}, fetchHdr)
	var got struct{ Errors map[string]string }
	if rec.Code != 422 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Errors["color_accent"] == "" {
		t.Fatalf("invalid fetch save: %d %s", rec.Code, rec.Body.String())
	}
	if string(site.cfg.ThemeExtra) != before || *flushed != 1 {
		t.Error("a rejected save changed the site or purged the cache")
	}
	if rec := g.do("POST", "/admin/theme/design-system", "editor-token", form, fetchHdr); rec.Code != 403 {
		t.Errorf("editor save: %d, want 403", rec.Code)
	}
	if rec := g.do("POST", "/admin/theme/design-system", "owner-token", form, nil); rec.Code != 303 || !hasFlash(rec) {
		t.Errorf("html save: %d flash=%v", rec.Code, hasFlash(rec))
	}
}

// Site styles is an owner feature: the flag and the current values only reach the owner's builder.
func TestBuilderBootCanStyleOwnerOnly(t *testing.T) {
	t.Parallel()

	boot := func(token string) map[string]json.RawMessage {
		r := newEditorRig(t)
		r.ent.Attributes = json.RawMessage(builderOnlyDoc)
		rec := r.do("GET", r.path(), token, nil, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: builder page %d", token, rec.Code)
		}
		m := bootRx.FindStringSubmatch(rec.Body.String())
		if m == nil {
			t.Fatalf("%s: no boot block", token)
		}
		var out map[string]json.RawMessage
		if err := json.Unmarshal([]byte(m[1]), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	owner := boot("owner-token")
	if string(owner["canStyle"]) != "true" {
		t.Fatalf("owner canStyle = %s", owner["canStyle"])
	}
	var ss struct{ Theme, Tokens map[string]string }
	if err := json.Unmarshal(owner["siteStyles"], &ss); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"primary", "secondary", "accent", "background", "text", "font", "heading_font", "radius_sm", "radius_md", "radius_lg"} {
		if ss.Theme[k] == "" {
			t.Errorf("siteStyles.theme lacks %q: %v", k, ss.Theme)
		}
	}
	for _, k := range []string{"color_surface", "color_muted", "color_accent"} {
		if !strings.HasPrefix(ss.Tokens[k], "#") {
			t.Errorf("siteStyles.tokens[%q] = %q", k, ss.Tokens[k])
		}
	}
	var urls map[string]string
	_ = json.Unmarshal(owner["urls"], &urls)
	if !strings.HasSuffix(urls["theme"], "/theme/colours") {
		t.Errorf("urls.theme = %q", urls["theme"])
	}

	editor := boot("editor-token")
	if string(editor["canStyle"]) != "false" {
		t.Errorf("editor canStyle = %s", editor["canStyle"])
	}
	if _, ok := editor["siteStyles"]; ok {
		t.Error("the editor's boot carries siteStyles")
	}
}

// The inert Theme colours say so, and the Design system page uses the same plain names as the builder.
func TestThemeLabelsShared(t *testing.T) {
	t.Parallel()

	_, pk, fp := newPackageRig(t)
	g := newRig(t, func(d *Deps) {
		d.Site = themeSite{newFakeSite("Acme Site")}
		d.Growth.Plugins, d.Growth.Packages = fp, pk
	})
	colours := g.do("GET", "/admin/theme/colours", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Theme accent (for themes that use it)", "Secondary colour (for themes that use it)", "Accent under Design system"} {
		if !strings.Contains(colours, want) {
			t.Errorf("Colours and fonts lacks %q", want)
		}
	}
	tokens := g.do("GET", "/admin/theme/design-system", "owner-token", nil, nil).Body.String()
	for _, name := range []string{"Surface", "Muted", "Accent"} {
		if !strings.Contains(tokens, ">"+name+"</label>") {
			t.Errorf("Design system lacks the label %q", name)
		}
	}
	if strings.Contains(tokens, "Colour accent") {
		t.Error("the old Colour accent label is still there")
	}
}

// The drawer posts only the fields it shows: the others keep what is stored now, not what the page booted with.
func TestThemeColoursFetchMergesOverStored(t *testing.T) {
	t.Parallel()

	_, pk, fp := newPackageRig(t)
	site := newFakeSite("Acme Site")
	g := newRig(t, func(d *Deps) { d.Site = themeSite{site}; d.Growth.Plugins, d.Growth.Packages = fp, pk })
	full := url.Values{"primary": {"#112233"}, "secondary": {"#445566"}, "accent": {"#778899"}, "background": {"#ffffff"}, "text": {"#000000"},
		"font": {"Inter, sans-serif"}, "heading_font": {"Georgia, serif"}, "radius_sm": {"2px"}, "radius_md": {"6px"}, "radius_lg": {"1rem"}}
	if rec := g.do("POST", "/admin/theme/colours", "owner-token", full, nil); rec.Code != 303 {
		t.Fatalf("seed: %d", rec.Code)
	}
	full.Set("font", "Courier, monospace") // another tab changes the font after the builder booted
	if rec := g.do("POST", "/admin/theme/colours", "owner-token", full, nil); rec.Code != 303 {
		t.Fatalf("font change: %d", rec.Code)
	}
	rec := g.do("POST", "/admin/theme/colours", "owner-token", url.Values{"primary": {"#abcdef"}}, fetchHdr)
	if rec.Code != 204 {
		t.Fatalf("drawer save: %d %s", rec.Code, rec.Body.String())
	}
	if site.cfg.PrimaryColor != "#abcdef" {
		t.Errorf("primary = %q", site.cfg.PrimaryColor)
	}
	if f := designFrom(&site.cfg); f.Font != "Courier, monospace" || f.HeadingFont != "Georgia, serif" || f.RadiusLg != "1rem" || f.Accent != "#778899" {
		t.Errorf("fields absent from the request changed: %+v", f)
	}
	// the merged form is validated like any other
	rec = g.do("POST", "/admin/theme/colours", "owner-token", url.Values{"primary": {"red"}}, fetchHdr)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), `"primary"`) {
		t.Errorf("invalid merged form: %d %s", rec.Code, rec.Body.String())
	}
}

func TestThemeTokensFetchMergesOverStored(t *testing.T) {
	t.Parallel()

	g, site, _ := tokensRig(t, true)
	if rec := g.do("POST", "/admin/theme/design-system", "owner-token", url.Values{"space_4": {"1.5rem"}, "fs_lg": {"1.4rem"}}, nil); rec.Code != 303 {
		t.Fatalf("seed: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/theme/design-system", "owner-token", url.Values{"color_accent": {"#112233"}}, fetchHdr); rec.Code != 204 {
		t.Fatalf("drawer save: %d", rec.Code)
	}
	d, _ := builder.ParseDesign(site.cfg.ThemeExtra)
	if d.Tokens.Color["accent"] != "#112233" || d.Tokens.Space["4"] != "1.5rem" || d.Tokens.FS["lg"] != "1.4rem" {
		t.Errorf("tokens absent from the request changed: %+v", d.Tokens)
	}
	// the HTML path is all-or-nothing: a field left out resets to the default
	if rec := g.do("POST", "/admin/theme/design-system", "owner-token", url.Values{"color_accent": {"#112233"}}, nil); rec.Code != 303 {
		t.Fatalf("html save: %d", rec.Code)
	}
	if d, _ := builder.ParseDesign(site.cfg.ThemeExtra); d.Tokens.Space["4"] != "" {
		t.Errorf("html save kept space_4: %+v", d.Tokens.Space)
	}
}

// X-Hm-Fetch is not a CSRF bypass: without the token both routes answer 403 (JSON in fetch mode) and store nothing.
func TestThemeFetchModeNeedsCSRF(t *testing.T) {
	t.Parallel()

	g, site, flushed := tokensRig(t, true)
	before := string(site.cfg.ThemeExtra)
	noToken := map[string]string{"X-Hm-Fetch": "1", "X-CSRF-Token": ""}
	for path, form := range map[string]url.Values{
		"/admin/theme/colours":       {"primary": {"#112233"}},
		"/admin/theme/design-system": {"color_accent": {"#112233"}},
	} {
		rec := g.do("POST", path, "owner-token", form, noToken)
		var got struct{ Code, Error string }
		if rec.Code != 403 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Code != "forbidden" || got.Error == "" {
			t.Errorf("%s: %d %s, want 403 {code,error}", path, rec.Code, rec.Body.String())
		}
	}
	if string(site.cfg.ThemeExtra) != before || site.cfg.PrimaryColor == "#112233" || *flushed != 0 {
		t.Error("a blocked save changed the site")
	}
	// an editor is refused in the same shape, the HTML path keeps the error page
	rec := g.do("POST", "/admin/theme/colours", "editor-token", url.Values{"primary": {"#112233"}}, fetchHdr)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), `"code":"forbidden"`) {
		t.Errorf("editor fetch: %d %s", rec.Code, rec.Body.String())
	}
	if rec := g.do("POST", "/admin/theme/colours", "editor-token", url.Values{"primary": {"#112233"}}, nil); rec.Code != 403 || !strings.Contains(rec.Body.String(), "Not allowed") {
		t.Errorf("editor html: %d", rec.Code)
	}
}
