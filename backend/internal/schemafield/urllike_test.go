package schemafield

import "testing"

func TestLooksLikeURLOrPath(t *testing.T) {
	yes := []string{
		"https://partner.example/flyer.jpg", "http://x", "ftp://x/y", "a://b",
		"/name.jpg", "//cdn.example/x", `\media\x`, "  /x", "/%6Dedia/x", "&#47;x",
		"name.jpg", "logo.PNG", "photo.jpeg?v=2", "a.webp#top", "clip.mp4", "doc.pdf", "icon.svg",
		"www.example.com/logo", "WWW.example.com",
		"data:text/html,x", "javascript:alert(1)", "vbscript:x", "file:x", "blob:x",
		"mailto:a@b.example", "tel:+123", `<a href="mailto:a@b">x</a>`,
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
		"a.pdfx", "Dr. Smith", "caller id: 5",
	}
	for _, s := range no {
		if LooksLikeURLOrPath(s) {
			t.Errorf("%q: want no match", s)
		}
	}
}
