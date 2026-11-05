package server

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/fonts"
	"hatamoto.dev/core/internal/siteconfig"
)

func f(family string, weight int, style, subset, hash string) fonts.Font {
	return fonts.Font{Family: family, Weight: weight, Style: style, Subset: subset, Hash: strings.Repeat(hash, 64)}
}

func TestPickFontFacesBodyAndHeading(t *testing.T) {
	list := []fonts.Font{
		f("Brand Sans", 700, "normal", "latin", "a"),
		f("Brand Sans", 400, "italic", "latin", "b"),
		f("Brand Sans", 500, "normal", "latin-ext", "c"),
		f("Brand Sans", 500, "normal", "latin", "d"),
		f("Other", 400, "normal", "latin", "e"),
		f("Inter", 400, "normal", "", "f"),
	}
	faces, preload := pickFontFaces(list, `"brand sans", sans-serif`, "Inter, sans-serif")
	if len(faces) != 5 {
		t.Fatalf("faces = %+v", faces)
	}
	for _, fc := range faces {
		if fc.Family == "Other" {
			t.Error("a font nobody uses was picked")
		}
	}
	// Body: normal style beats italic, 500 is closest to 400 among normals,
	// and latin beats latin-ext. The heading font is never preloaded.
	if want := strings.Repeat("d", 64); preload != want {
		t.Errorf("preload = %q, want %q", preload, want)
	}
}

func TestPickFontFacesHeadingOnlyHasNoPreload(t *testing.T) {
	list := []fonts.Font{f("Display", 700, "normal", "latin", "a")}
	faces, preload := pickFontFaces(list, "Inter, sans-serif", `'Display', serif`)
	if len(faces) != 1 || preload != "" {
		t.Errorf("faces %+v preload %q", faces, preload)
	}
}

func TestPickFontFacesNothingInstalled(t *testing.T) {
	list := []fonts.Font{f("Brand Sans", 400, "normal", "latin", "a")}
	for _, c := range [][2]string{
		{"Inter, sans-serif", "Inter, sans-serif"}, // default stack, nothing installed under that name
		{"Uninstalled, serif", ""},
		{"", ""},
		{"system-ui, sans-serif", "Georgia, serif"},
	} {
		if faces, preload := pickFontFaces(list, c[0], c[1]); len(faces) != 0 || preload != "" {
			t.Errorf("%v: faces %+v preload %q", c, faces, preload)
		}
	}
	if faces, _ := pickFontFaces(nil, "Brand Sans", ""); len(faces) != 0 {
		t.Error("empty list gave faces")
	}
}

func TestFirstFamily(t *testing.T) {
	for in, want := range map[string]string{
		`"Brand Sans", sans-serif`: "Brand Sans", `'X'`: "X", ` Inter ,serif`: "Inter", "": "",
	} {
		if got := firstFamily(in); got != want {
			t.Errorf("firstFamily(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFontsNilStoreGivesNoFaces(t *testing.T) {
	s := &Server{}
	if faces, preload := s.fontFaces(t.Context(), siteconfig.ThemeConfig{}); faces != nil || preload != "" {
		t.Error("nil store must give no fonts")
	}
}
