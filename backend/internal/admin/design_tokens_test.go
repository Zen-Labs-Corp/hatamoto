package admin

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

func tokensRig(t *testing.T, optedIn bool) (*rig, themeSite, *int) {
	t.Helper()
	_, pk, fp := newPackageRig(t)
	site := themeSite{newFakeSite("Acme Site")}
	site.cfg.ThemeExtra = json.RawMessage(`{"colors":{"accent":"#778899"},"socialLinks":{"github":"https://g.example"},"builder":{"presets":{"card":{"label":"Card","s":{"base":{"pt":"space-4"}}}}}}`)
	flushed := new(int)
	g := newRig(t, func(d *Deps) {
		d.Site = site
		d.Growth.Plugins, d.Growth.Packages = fp, pk
		d.Invalidate = func(context.Context) { *flushed++ }
		d.BuilderTokens = func() bool { return optedIn }
	})
	return g, site, flushed
}

func TestTokensSaveMergesPurgesAndKeepsOtherKeys(t *testing.T) {
	t.Parallel()

	g, site, flushed := tokensRig(t, true)
	rec := g.do("GET", "/admin/theme/design-system", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Surface") || !strings.Contains(body, `value="#ffffff"`) {
		t.Fatalf("form: %d\n%s", rec.Code, body)
	}
	if strings.Contains(body, "has not turned on design tokens") {
		t.Error("opt-in notice shown for an opted-in theme")
	}
	a11yOK(t, body)
	compareGolden(t, "pages/settings-design-tokens.html", normalizePage(body))

	form := url.Values{"color_accent": {"#112233"}, "color_surface": {"#fff"}, "space_4": {"1.25rem"}, "space_2": {"0.5rem"}, "fs_lg": {"1.4rem"}, "shadow_md": {"0 2px 6px rgba(0,0,0,.2)"}}
	if rec := g.do("POST", "/admin/theme/design-system", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if *flushed != 1 {
		t.Errorf("rendered cache purged %d times, want 1", *flushed)
	}
	var got map[string]json.RawMessage
	_ = json.Unmarshal(site.cfg.ThemeExtra, &got)
	for _, key := range []string{"colors", "socialLinks", "builder"} {
		if _, ok := got[key]; !ok {
			t.Errorf("lost %q: %s", key, site.cfg.ThemeExtra)
		}
	}
	d, err := builder.ParseDesign(site.cfg.ThemeExtra)
	if err != nil {
		t.Fatal(err)
	}
	// defaults (surface #fff, space_2 0.5rem) are not stored; changed values are
	if d.Tokens.Color["accent"] != "#112233" || d.Tokens.Space["4"] != "1.25rem" || d.Tokens.FS["lg"] != "1.4rem" || d.Tokens.Shadow["md"] != "0 2px 6px rgba(0,0,0,.2)" {
		t.Fatalf("tokens: %+v", d.Tokens)
	}
	if _, ok := d.Tokens.Color["surface"]; ok || len(d.Tokens.Space) != 1 {
		t.Errorf("defaults stored: %+v", d.Tokens)
	}
	if d.Presets["card"].Label != "Card" {
		t.Errorf("presets lost: %+v", d.Presets)
	}
	if rec := g.do("GET", "/admin/theme/design-system", "owner-token", nil, nil); !strings.Contains(rec.Body.String(), `value="#112233"`) || !strings.Contains(rec.Body.String(), `value="1.25rem"`) {
		t.Error("saved values not shown on the form")
	}
}

func TestTokensInvalidValueIs422(t *testing.T) {
	t.Parallel()

	g, site, flushed := tokensRig(t, true)
	before := string(site.cfg.ThemeExtra)
	form := url.Values{"color_accent": {"red"}, "space_4": {"1px;}</style>"}, "shadow_md": {"0 1px red"}, "fs_lg": {"2rem"}}
	rec := g.do("POST", "/admin/theme/design-system", "owner-token", form, nil)
	body := rec.Body.String()
	if rec.Code != 422 || strings.Count(body, "hm-field__error") != 3 {
		t.Fatalf("invalid: %d, errors %d", rec.Code, strings.Count(body, "hm-field__error"))
	}
	if strings.Contains(body, "</style>") && strings.Contains(body, "1px;}</style>") {
		t.Error("hostile value echoed unescaped")
	}
	if !strings.Contains(body, `value="2rem"`) {
		t.Error("valid sibling value lost on error")
	}
	a11yOK(t, body)
	if string(site.cfg.ThemeExtra) != before || *flushed != 0 {
		t.Error("an invalid save changed state")
	}
}

func TestTokensReset(t *testing.T) {
	t.Parallel()

	g, site, flushed := tokensRig(t, true)
	g.do("POST", "/admin/theme/design-system", "owner-token", url.Values{"space_4": {"2rem"}}, nil)
	if rec := g.do("POST", "/admin/theme/design-system/reset", "owner-token", url.Values{}, nil); rec.Code != 303 {
		t.Fatalf("reset: %d", rec.Code)
	}
	d, _ := builder.ParseDesign(site.cfg.ThemeExtra)
	if len(d.Tokens.Space) != 0 || d.Presets["card"].Label != "Card" || *flushed != 2 {
		t.Fatalf("after reset: %+v presets=%v purges=%d", d.Tokens, d.Presets, *flushed)
	}
}

func TestTokensOptInNoticeAndOwnerOnly(t *testing.T) {
	t.Parallel()

	g, _, _ := tokensRig(t, false)
	rec := g.do("GET", "/admin/theme/design-system", "owner-token", nil, nil)
	if !strings.Contains(rec.Body.String(), "Your theme has not turned on design tokens (theme.json builder.tokens)") {
		t.Error("opt-in notice missing")
	}
	a11yOK(t, rec.Body.String())
	for _, m := range []string{"GET", "POST"} {
		for _, p := range []string{"/admin/theme/design-system", "/admin/theme/design-system/reset"} {
			if m == "GET" && strings.HasSuffix(p, "reset") {
				continue
			}
			if rec := g.do(m, p, "editor-token", url.Values{"space_4": {"2rem"}}, nil); rec.Code != 403 {
				t.Errorf("editor %s %s: %d", m, p, rec.Code)
			}
		}
	}
	if rec := g.do("POST", "/admin/theme/design-system", "owner-token", url.Values{"space_4": {"2rem"}}, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
		t.Errorf("missing CSRF token: %d", rec.Code)
	}
}
