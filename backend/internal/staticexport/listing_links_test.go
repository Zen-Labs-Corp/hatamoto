package staticexport

import (
	"net/url"
	"testing"
)

func TestRewriteListingLinks(t *testing.T) {
	doc, _ := url.Parse("https://example.com/blog?page=2")
	for _, c := range []struct{ name, in, want string }{
		{"root-relative", `<a href="/blog?page=2">2</a>`, `<a href="/blog/page/2/">2</a>`},
		{"relative", `<a href="?page=3">3</a>`, `<a href="/blog/page/3/">3</a>`},
		{"absolute link next", `<link rel="next" href="https://example.com/blog?page=2">`, `<link rel="next" href="https://example.com/blog/page/2/">`},
		{"rel prev among others", `<link rel="prefetch prev" href="/blog?page=1">`, `<link rel="prefetch prev" href="/blog">`},
		{"page one", `<a href="/blog?page=1">1</a>`, `<a href="/blog">1</a>`},
		{"root listing", `<a href="/?page=2">`, `<a href="/page/2/">`},
		{"fragment", `<a href="/blog?page=2#top">`, `<a href="/blog/page/2/#top">`},
		{"single quoted", `<a href='/blog?page=2'>`, `<a href="/blog/page/2/">`},
		{"unquoted", `<a class=x href=/blog?page=2 id=y>`, `<a class=x href="/blog/page/2/" id=y>`},
		{"entity query", `<a href="/blog?page=2" title="a &amp; b">`, `<a href="/blog/page/2/" title="a &amp; b">`},
		{"two links", `<p>x &amp; y</p><a href="/blog?page=2">a</a> <a href="/blog?page=3">b</a>`, `<p>x &amp; y</p><a href="/blog/page/2/">a</a> <a href="/blog/page/3/">b</a>`},
	} {
		if got := string(RewriteListingLinks(doc, []byte(c.in))); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRewriteListingLinksUntouched(t *testing.T) {
	doc, _ := url.Parse("https://example.com/blog")
	for _, in := range []string{
		`<link rel="canonical" href="https://example.com/blog?page=2">`,
		`<meta property="og:url" content="https://example.com/blog?page=2">`,
		`<script type="application/ld+json">{"url":"https://example.com/blog?page=2"}</script>`,
		`<a href="https://other.example/blog?page=2">x</a>`,
		`<a href="/search?q=a&amp;page=2">x</a>`,
		`<a href="/blog?page=2&amp;x=1">x</a>`,
		`<a data-href="/blog?page=2">x</a>`,
		`<script>var u = '<a href="/blog?page=2">';</script>`,
		`<a href="/blog?schema=a">x</a><p>&amp; plain</p>`,
	} {
		b := []byte(in)
		got := RewriteListingLinks(doc, b)
		if string(got) != in || &got[0] != &b[0] {
			t.Errorf("changed %q -> %q", in, got)
		}
	}
}
