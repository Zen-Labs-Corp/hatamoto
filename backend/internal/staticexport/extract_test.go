package staticexport

import (
	"net/url"
	"slices"
	"testing"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func wantLinks(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("links = %v\nwant    %v", got, want)
	}
}

func TestLinks_HTML(t *testing.T) {
	body := `<!doctype html><html><head>
<link rel="preload" as="font" href="/fonts/f.woff2">
<link rel="stylesheet" href="/theme/assets/theme.abc.css">
<meta property="og:image" content="https://example.com/media/og.jpg">
<meta name="twitter:image" content="https://other.example.net/x.jpg">
<style>.h{background:url(/media/bg.png)}</style>
</head><body style="background:url('/media/inline.png')">
<a href="/blog/x#top">x</a>
<a href="/blog?page=2">next</a>
<a href="https://elsewhere.example.net/y">out</a>
<a href="mailto:a@example.com">mail</a>
<a href="about">rel</a>
<img src="/media/a.jpg" srcset="/media/a-300.webp 300w, /media/a-600.webp 600w">
<picture><source srcset="/media/p.avif 1x"><img src="data:image/gif;base64,AAAA"></picture>
<script src="/theme/assets/theme.abc.js"></script>
</body></html>`
	got := Links(mustURL(t, "https://example.com/blog/post"), "text/html; charset=utf-8", []byte(body))
	wantLinks(t, got, []string{
		"/blog/about", "/blog/x", "/blog?page=2", "/fonts/f.woff2",
		"/media/a-300.webp", "/media/a-600.webp", "/media/a.jpg", "/media/bg.png",
		"/media/inline.png", "/media/og.jpg", "/media/p.avif",
		"/theme/assets/theme.abc.css", "/theme/assets/theme.abc.js",
	})
}

func TestLinks_CSS(t *testing.T) {
	css := `@import "/theme/assets/base.css";
@font-face{src:url("/fonts/` + "aa" + `.woff2") format("woff2")}
.i{background:url(icons/a.svg)}
.d{background:url(data:image/png;base64,AAA)}`
	got := Links(mustURL(t, "https://example.com/theme/assets/theme.x.css"), "text/css", []byte(css))
	wantLinks(t, got, []string{"/fonts/aa.woff2", "/theme/assets/base.css", "/theme/assets/icons/a.svg"})
}

func TestLinks_XMLSitemapAndFeed(t *testing.T) {
	xml := `<sitemapindex><sitemap><loc>https://example.com/sitemap-pages.xml</loc></sitemap></sitemapindex>
<urlset><url><loc>https://example.com/about?a=1&amp;b=2</loc>
<image:image><image:loc>https://example.com/media/a.jpg</image:loc></image:image></url>
<url><loc>https://other.example.net/x</loc></url></urlset>`
	got := Links(mustURL(t, "https://example.com/sitemap.xml"), "application/xml", []byte(xml))
	wantLinks(t, got, []string{"/about?a=1&b=2", "/media/a.jpg", "/sitemap-pages.xml"})
}

func TestLinks_LLMSTxt(t *testing.T) {
	txt := "# Site\n- [Graph](https://example.com/api/v1/public/knowledge-graph)\n" +
		"- Posts: https://example.com/api/v1/public/knowledge-graph?schema=post.\n"
	got := Links(mustURL(t, "https://example.com/llms.txt"), "text/plain; charset=utf-8", []byte(txt))
	wantLinks(t, got, []string{"/api/v1/public/knowledge-graph", "/api/v1/public/knowledge-graph?schema=post"})
}
