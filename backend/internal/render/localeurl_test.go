package render

import (
	"strings"
	"testing"
)

func TestLocalizeURL(t *testing.T) {
	t.Parallel()
	l := LocaleLinks{
		Segment:  "fr",
		Segments: []string{"fr", "es"},
		Shared:   func(p string) bool { return strings.HasPrefix(p, "/media") || p == "/people/x" },
	}
	tests := []struct{ in, want string }{
		{"/contact", "/fr/contact"},
		{"/contact?x=1#a", "/fr/contact?x=1#a"},
		{"/", "/fr"},
		{"/?a=1", "/fr?a=1"},
		{"/fr/x", "/fr/x"},
		{"/es/x", "/es/x"},
		{"/fr", "/fr"},
		{"/FR/x", "/FR/x"},
		{"https://x.io/a", "https://x.io/a"},
		{"//x.io", "//x.io"},
		{`/\x.io`, `/\x.io`},
		{"#top", "#top"},
		{"?q=1", "?q=1"},
		{"mailto:a@x.io", "mailto:a@x.io"},
		{"tel:+1", "tel:+1"},
		{"", ""},
		{"relative/x", "relative/x"},
		{"/media/a.png", "/media/a.png"},
		{"/people/x", "/people/x"},
		{"/people/y", "/fr/people/y"},
	}
	for _, tc := range tests {
		if got := LocalizeURL(l, tc.in); got != tc.want {
			t.Errorf("LocalizeURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := LocalizeURL(LocaleLinks{}, "/contact"); got != "/contact" {
		t.Errorf("empty segment rewrote the URL: %q", got)
	}
}

func TestPageViewLocaleLinks(t *testing.T) {
	t.Parallel()
	v := &PageView{
		Segment: "fr",
		Locales: []LocaleSummary{{Tag: "en"}, {Tag: "fr", Segment: "fr"}, {Tag: "es", Segment: "es"}},
	}
	l := v.LocaleLinks()
	if l.Segment != "fr" || len(l.Segments) != 2 {
		t.Errorf("LocaleLinks = %+v", l)
	}
	var nilView *PageView
	if got := nilView.LocaleLinks(); got.Segment != "" {
		t.Errorf("nil view LocaleLinks = %+v", got)
	}
}
