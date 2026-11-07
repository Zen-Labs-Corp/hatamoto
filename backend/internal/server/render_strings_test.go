package server

import (
	"bytes"
	"context"
	"io/fs"
	"maps"
	"testing"
	"testing/fstest"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

const stringsExtra = `{"colors":{},"themeStrings":{"st":{` +
	`"en":{"hello":"Hello override","bye":"Bye override"},` +
	`"fr":{"hello":"Bonjour"}},"other":{"fr":{"hello":"nope"}}}}`

func TestThemeStringOverridesChain(t *testing.T) {
	extra := []byte(stringsExtra)
	cases := []struct {
		name, theme, locale string
		want                map[string]string
	}{
		{"locale over default", "st", "fr", map[string]string{"hello": "Bonjour", "bye": "Bye override"}},
		{"default locale page", "st", "en", map[string]string{"hello": "Hello override", "bye": "Bye override"}},
		{"single-locale install: empty locale is the default tag", "st", "", map[string]string{"hello": "Hello override", "bye": "Bye override"}},
		{"locale without overrides falls to the default tag", "st", "es", map[string]string{"hello": "Hello override", "bye": "Bye override"}},
		{"other theme is not mixed in", "none", "fr", nil},
	}
	for _, c := range cases {
		if got := themeStringOverrides(extra, c.theme, c.locale, "en"); !maps.Equal(got, c.want) {
			t.Errorf("%s: %v", c.name, got)
		}
	}
	if got := themeStringOverrides(nil, "st", "fr", "en"); got != nil {
		t.Errorf("no extra: %v", got)
	}
}

func TestDefaultStringsTag(t *testing.T) {
	multi, err := entity.NewLocaleSet("fr", []entity.LocaleEntry{{Tag: "fr", Segment: "fr"}, {Tag: "es", Segment: "es"}}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := defaultStringsTag(multi, "es"); got != "fr" {
		t.Errorf("multi: %q", got)
	}
	if got := defaultStringsTag(&entity.LocaleSet{}, "es"); got != "es" {
		t.Errorf("single: %q", got)
	}
	if got := defaultStringsTag(nil, "not a tag"); got != "not a tag" {
		t.Errorf("fallback: %q", got)
	}
}

// The overrides reach rendered pages of every kind the engine draws, through
// the same PageView field applyStrings fills.
func TestStringOverridesRenderOnEveryPageKind(t *testing.T) {
	theme := fstest.MapFS{
		"theme.json":        {Data: []byte(`{"name":"st","version":"1.0.0","strings":{"hello":"Hello default","bye":"Bye default"}}`)},
		"layouts/base.html": {Data: []byte(`{{t . "hello"}}/{{t . "bye"}}`)},
	}
	e, err := render.New(render.Options{Layers: []fs.FS{theme}})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.ThemeName(); got != "st" {
		t.Fatalf("ThemeName = %q", got)
	}
	extra := []byte(stringsExtra)
	kinds := map[string]func(v *render.PageView, b *bytes.Buffer) error{
		"page":      func(v *render.PageView, b *bytes.Buffer) error { return e.RenderPage(b, v) },
		"not found": func(v *render.PageView, b *bytes.Buffer) error { return e.RenderNotFound(b, v) },
		"error":     func(v *render.PageView, b *bytes.Buffer) error { return e.RenderError(b, v, 500) },
		"access":    func(v *render.PageView, b *bytes.Buffer) error { return e.RenderAccess(b, v, 401) },
	}
	for name, draw := range kinds {
		for locale, want := range map[string]string{"fr": "Bonjour/Bye override", "": "Hello override/Bye override"} {
			v := &render.PageView{BaseURL: "https://example.test", Path: "/", Locale: locale}
			v.StringOverrides = themeStringOverrides(extra, e.ThemeName(), v.Locale, "en")
			var buf bytes.Buffer
			if err := draw(v, &buf); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if buf.String() != want {
				t.Errorf("%s locale %q: %q, want %q", name, locale, buf.String(), want)
			}
		}
	}
}

func TestStringsThemeFollowsPreview(t *testing.T) {
	ctx := context.Background()
	if got := stringsTheme(ctx, "active"); got != "active" {
		t.Errorf("no preview: %q", got)
	}
	if got := stringsTheme(withPreviewTheme(ctx, "other"), "active"); got != "other" {
		t.Errorf("preview: %q", got)
	}
}
