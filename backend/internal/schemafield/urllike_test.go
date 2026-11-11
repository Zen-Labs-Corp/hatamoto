package schemafield

import "testing"

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
		`<p class="lead">Doors 7pm</p><h2>Line-up</h2>`, `<a href="#top" title="Tickets">x</a>`, `<p>5 / 6</p>`,
	}
	for _, s := range no {
		if LooksLikeURLOrPath(s) {
			t.Errorf("%q: want no match", s)
		}
	}
}
