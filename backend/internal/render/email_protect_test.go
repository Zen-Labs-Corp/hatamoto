package render

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

const emailPage = `<!doctype html><html><head><title>hi@example.org</title><script type="application/ld+json">{"email":"hi@example.org"}</script></head><body>%s</body></html>`

func wrap(body string) string { return strings.Replace(emailPage, "%s", body, 1) }

func TestEmailProtectEncodes(t *testing.T) {
	out := string(protectEmails([]byte(wrap(`<p><a href="mailto:hi@example.org">hi@example.org</a></p>`))))
	body := out[strings.Index(out, "<body>"):]
	if strings.Contains(body, "hi@example.org") || strings.Contains(strings.ToLower(body), "mailto:") {
		t.Fatalf("body still literal: %s", body)
	}
	if !strings.Contains(out, `{"email":"hi@example.org"}`) || !strings.Contains(out, "<title>hi@example.org</title>") {
		t.Errorf("head changed: %s", out)
	}
	doc, err := html.Parse(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	var href, text string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key == "href" {
					href = a.Val
				}
			}
			text = n.FirstChild.Data
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if href != "mailto:hi@example.org" || text != "hi@example.org" {
		t.Errorf("parsed href=%q text=%q", href, text)
	}
}

func TestEmailProtectHrefForms(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"upper", `<a href="MAILTO:a@b.org">x</a>`},
		{"query", `<a href="mailto:a@b.org?subject=a&amp;body=b">x</a>`},
		{"unquoted", `<a href=mailto:a@b.org>x</a>`},
		{"single", `<a class="c" href='mailto:a@b.org' id=z>x</a>`},
	} {
		out := string(protectEmails([]byte(tc.in)))
		if strings.Contains(strings.ToLower(out), "mailto") || strings.Contains(out, "a@b.org") {
			t.Errorf("%s: literal left: %s", tc.name, out)
		}
		if !strings.HasSuffix(out, ">x</a>") {
			t.Errorf("%s: tail changed: %s", tc.name, out)
		}
	}
	got := string(protectEmails([]byte(`<a href="mailto:a@b.org?subject=a&amp;body=b">x</a>`)))
	if !strings.Contains(got, "&#63;subject&#61;a&#38;body") && !strings.Contains(got, "&#63;&#115;") {
		t.Errorf("query not encoded as references: %s", got)
	}
}

func TestEmailProtectLiteralZones(t *testing.T) {
	for _, in := range []string{
		`<script>var a="a@b.org"</script>`,
		`<style>/* a@b.org */</style>`,
		`<textarea>a@b.org</textarea>`,
		`<code>a@b.org</code>`,
		`<pre>a@b.org <b>a@b.org</b></pre>`,
		`<!-- a@b.org -->`,
		`<input value="a@b.org"><meta content="a@b.org"><div data-m="a@b.org"></div><img alt="a@b.org">`,
		`<a href="tel:+41790000000">+41</a>`,
		`<a title=" href=mailto:a@b.org" href="/x">x</a>`,
		`<p>photo@2x.png and @handle</p>`,
	} {
		if out := string(protectEmails([]byte(in))); out != in {
			t.Errorf("changed:\n in: %s\nout: %s", in, out)
		}
	}
}

func TestEmailProtectIdentity(t *testing.T) {
	in := wrap(`<p class=a  id="b">Hello &amp; welcome <br/><img src=x.png alt="y"></p><!-- c --><a href="/mailing">mail</a>`)
	if out := protectEmails([]byte(in)); !bytes.Equal(out, []byte(in)) {
		t.Errorf("not identical: %s", out)
	}
	in2 := wrap(`<p>Write to <b>x@y.org</b>, class="k"  <i  >z</i></p>`)
	out := string(protectEmails([]byte(in2)))
	if strings.Contains(out, "x@y.org") || !strings.Contains(out, `, class="k"  <i  >z</i></p>`) {
		t.Errorf("surroundings changed: %s", out)
	}
}

func FuzzProtectEmails(f *testing.F) {
	f.Add(wrap(`<a href="mailto:a@b.org">a@b.org</a>`))
	f.Add(`<a href=`)
	f.Add(`<p>a@b.co<script>a@b.co`)
	f.Fuzz(func(t *testing.T, s string) {
		out := protectEmails([]byte(s))
		if _, err := html.Parse(bytes.NewReader(out)); err != nil {
			t.Fatal(err)
		}
		if !emailAddr.MatchString(s) && !containsFold([]byte(s), "mailto") && !bytes.Equal(out, []byte(s)) {
			t.Fatalf("changed without a match: %q -> %q", s, out)
		}
		if !emailAddr.MatchString(s) && !containsFold([]byte(s), "mailto") {
			return
		}
		again := protectEmails(out)
		if !bytes.Equal(protectEmails(again), again) {
			t.Fatalf("not stable: %q", out)
		}
	})
}

func BenchmarkProtectEmails(b *testing.B) {
	in := []byte(wrap(strings.Repeat(`<p>Text <a href="/x">link</a> and a@b.org</p>`, 500)))
	b.SetBytes(int64(len(in)))
	for b.Loop() {
		protectEmails(in)
	}
}
