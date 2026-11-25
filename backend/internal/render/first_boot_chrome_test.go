package render

import (
	"strings"
	"testing"
)

func TestLogoMonogram(t *testing.T) {
	const mark = `<span class="hm-logo__mark" aria-hidden="true">`
	tests := []struct {
		name, site, want string
	}{
		{"ascii", "Angula", mark + "A</span>"},
		{"accented", "élan", mark + "É</span>"},
		{"empty", "", ""},
		{"digits", "123", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := minimalView()
			v.Site.Name = tc.site
			out := renderShell(t, v)
			if tc.want == "" {
				if strings.Contains(out, "hm-logo__mark") {
					t.Errorf("unexpected monogram for %q:\n%s", tc.site, out)
				}
				return
			}
			if got := strings.Count(out, tc.want); got < 1 {
				t.Errorf("want monogram %s in:\n%s", tc.want, out)
			}
			if strings.Contains(out, "旗本") {
				t.Error("page still carries the old kanji mark")
			}
		})
	}
	t.Run("logo image", func(t *testing.T) {
		v := minimalView()
		v.Site.LogoURL = "/media/logo.png"
		out := renderShell(t, v)
		if !strings.Contains(out, "<img") || strings.Contains(out, "hm-logo__mark") {
			t.Errorf("a logo image must replace the monogram:\n%s", out)
		}
	})
}
