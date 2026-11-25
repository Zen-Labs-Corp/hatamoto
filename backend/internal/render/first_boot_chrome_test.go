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

func footerOf(t *testing.T, v *PageView) string {
	t.Helper()
	out := renderShell(t, v)
	i := strings.Index(out, `<footer class="hm-footer">`)
	if i < 0 {
		t.Fatalf("no footer in:\n%s", out)
	}
	return out[i:]
}

func TestFooterChrome(t *testing.T) {
	nav := []NavItem{{Label: "Docs", Href: "/docs"}, {Label: "About", Href: "/about"}}
	rss := []FeedLink{{Title: "Field Notes", Type: "application/rss+xml", Href: "/feed.xml"}}

	t.Run("tagline", func(t *testing.T) {
		v := minimalView()
		v.Site.Tagline = "Notes from the field"
		if f := footerOf(t, v); !strings.Contains(f, `<p class="hm-footer__desc">Notes from the field</p>`) {
			t.Errorf("missing tagline:\n%s", f)
		}
		v.Site.Tagline = ""
		if f := footerOf(t, v); strings.Contains(f, "hm-footer__desc") {
			t.Errorf("empty tagline rendered a description:\n%s", f)
		}
	})
	t.Run("pages column falls back to the header nav", func(t *testing.T) {
		v := minimalView()
		v.Site.Nav = nav
		f := footerOf(t, v)
		for _, want := range []string{`<p class="hm-footer__title">Pages</p>`, `>Docs</a>`, `>About</a>`} {
			if !strings.Contains(f, want) {
				t.Errorf("missing %s in:\n%s", want, f)
			}
		}
	})
	t.Run("footer nav wins and keeps group titles", func(t *testing.T) {
		v := minimalView()
		v.Site.Nav = nav
		v.Site.FooterNav = []NavItem{{Label: "Company", Children: []NavItem{{Label: "Team", Href: "/team"}}}}
		f := footerOf(t, v)
		if !strings.Contains(f, `<p class="hm-footer__title">Company</p>`) {
			t.Errorf("missing group title:\n%s", f)
		}
		if strings.Contains(f, ">Docs</a>") || strings.Contains(f, ">Pages</p>") {
			t.Errorf("header nav leaked into a footer with its own menu:\n%s", f)
		}
	})
	t.Run("follow column", func(t *testing.T) {
		v := minimalView()
		f := footerOf(t, v)
		if strings.Contains(f, "/feed.xml") || !strings.Contains(f, `<a href="/sitemap.xml">Sitemap</a>`) {
			t.Errorf("want sitemap only without feeds:\n%s", f)
		}
		v.Site.Feeds = rss
		if f := footerOf(t, v); !strings.Contains(f, `<a href="/feed.xml">RSS feed</a>`) {
			t.Errorf("missing RSS link:\n%s", f)
		}
	})
	t.Run("credit", func(t *testing.T) {
		v := minimalView()
		want := `<p class="hm-footer__credit"><a href="https://hatamoto.dev">Powered by Hatamoto</a></p>`
		if f := footerOf(t, v); !strings.Contains(f, want) {
			t.Errorf("missing credit:\n%s", f)
		}
		v.StringOverrides = map[string]string{"powered_by_url": "https://example.test/"}
		if f := footerOf(t, v); !strings.Contains(f, `<a href="https://example.test/">Powered by Hatamoto</a>`) {
			t.Errorf("credit URL override ignored:\n%s", f)
		}
	})
	t.Run("brand", func(t *testing.T) {
		v := minimalView()
		v.Site.Name = "Angula"
		f := footerOf(t, v)
		if !strings.Contains(f, `<span class="hm-logo__mark" aria-hidden="true">A</span>`) {
			t.Errorf("footer lacks the monogram:\n%s", f)
		}
	})
}
