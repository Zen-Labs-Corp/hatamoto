package schemafield

import (
	"html"
	"testing"
)

func TestLooksLikeURLOrPath(t *testing.T) {
	yes := []string{
		"https://partner.example/flyer.jpg", "http://x", "ftp://x/y", "a://b",
		"/name.jpg", "//cdn.example/x", `\media\x`, "  /x", "/%6Dedia/x", "&#47;x",
		"name.jpg", "logo.PNG", "photo.jpeg?v=2", "a.webp#top", "clip.mp4", "doc.pdf", "icon.svg",
		"www.example.com/logo", "WWW.example.com",
		"data:text/html,x", "javascript:alert(1)", "vbscript:x", "file:x", "blob:x",
		"mailto:a@b.example", "tel:+123", "Call tel:+44 20 7946 0000", "TEL:+1", "sms:123", "about:blank", "data:text/html,x", `<a href="mailto:a@b">x</a>`,
		`<a href="//partner.example/buy">x</a>`, `<a href="/tickets">x</a>`, `<a href="https://site-linked-host/x">x</a>`,
		`<p class="lead">x</p><a href="/tickets">x</a>`, `<form action="//x.example/p"><button formaction="/go">x</button></form>`,
		`<blockquote cite="/src">x</blockquote>`, `<svg><a xlink:href="//x.example/y">x</a></svg>`, `<object data="/f">x</object>`,
		`<a href="&#47;tickets">x</a>`, `<A HREF="//Partner.example">x</A>`,
		`<a href="tickets">x</a>`, `<a href="../../shop/x">x</a>`, `<a href="?aff=x">x</a>`, `<a href="#more">x</a>`,
		`<a data-entity-id="0b3c1d52-0000-4000-8000-000000000000">x</a>`, `<img SRC=x>`, `<p ping="x">x</p>`,
		`<a x="http://h">x</a>`,
		`background:image-set("x.png" 1x)`, "image-set ('a')", "url(x)", "URL (x)",
	}
	for _, s := range yes {
		if !LooksLikeURLOrPath(s) {
			t.Errorf("%q: want a match", s)
		}
	}
	no := []string{
		"", "  ", "Doors: 7pm", "19:30", "Wow, 3 pm", "see page 3/4", "Profile: done", "hotel: open",
		"Hello world.", "e.g. a jpg file", "Price 12.50", "2026-11-10", "draft", "<p>Hello <strong>you</strong>.</p>",
		"Tel: 020 7946 0000", "About: the venue", "Data: n/a", "File: see desk", "SMS: 123 456", "Mailto: a@b", "a.pdfx", "Dr. Smith", "caller id: 5",
		`<p class="lead">Doors 7pm</p><h2>Line-up</h2>`, `<a id="top" title="Tickets">x</a>`,
		`<h2 id="line-up">x</h2>`, `<p>5 / 6</p>`,
	}
	for _, s := range no {
		if LooksLikeURLOrPath(s) {
			t.Errorf("%q: want no match", s)
		}
	}
}

// nestedMarkup is `<a x="` + escape(inner) + `">` repeated depth times around
// base: the cycle-8 value that made a recursive scan exponential.
func nestedMarkup(depth int, base string) string {
	s := base
	for i := 0; i < depth; i++ {
		s = `<a x="` + html.EscapeString(s) + `">`
	}
	return s
}

func TestLooksLikeURLOrPathNestedMarkupIsLinear(t *testing.T) {
	s := nestedMarkup(120, "x")
	if len(s) < 100_000 {
		t.Fatalf("test value too small: %d bytes", len(s))
	}
	scanned := 0
	scanHook = func(n int) { scanned += n }
	defer func() { scanHook = nil }()
	lu, im := LooksLikeURLOrPath(s), MentionsImageMarkup(s)
	if scanned > 40*len(s) {
		t.Errorf("scanned %d bytes for a %d byte value: work is not linear", scanned, len(s))
	}
	if lu || im {
		t.Errorf("nested plain value: LooksLikeURLOrPath=%v MentionsImageMarkup=%v, want false", lu, im)
	}
}

func TestLooksLikeURLOrPathNestedLink(t *testing.T) {
	// A link in the innermost value is found while it is within the decode bound.
	if !LooksLikeURLOrPath(nestedMarkup(3, "http://h")) {
		t.Error("shallow nested link not found")
	}
	// Markup inside an attribute value is not parsed again, as browsers do not.
	if LooksLikeURLOrPath(nestedMarkup(3, `<a href=tickets>`)) {
		t.Error("link inside a nested attribute value should not be parsed as markup")
	}
}
