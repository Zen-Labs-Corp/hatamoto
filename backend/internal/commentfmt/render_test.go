package commentfmt

import (
	"errors"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, want string }{
		{"marks", "*a* **b** `c`", "<p><em>a</em> <strong>b</strong> <code>c</code></p>"},
		{"code keeps markup literal", "`*x* <b>`", "<p><code>*x* &lt;b&gt;</code></p>"},
		{"indented block", "    <i>x</i>\n    y", "<pre><code>&lt;i&gt;x&lt;/i&gt;\ny</code></pre>"},
		{"quote", "> q", "<blockquote><p>q</p></blockquote>"},
		{"line break and paragraphs", "a\nb\n\nc", "<p>a<br>b</p><p>c</p>"},
		{"raw html escaped", "<b>x</b>", "<p>&lt;b&gt;x&lt;/b&gt;</p>"},
		{"autolink trims dot", "https://example.com/a.", `<p><a href="https://example.com/a" rel="ugc nofollow noopener">https://example.com/a</a>.</p>`},
		{"www gets scheme in href", "www.example.com", `<p><a href="http://www.example.com" rel="ugc nofollow noopener">www.example.com</a></p>`},
		{"markdown link renders its URL", "[x](https://example.com)", `<p><a href="https://example.com" rel="ugc nofollow noopener">https://example.com</a></p>`},
		{"javascript not linked", "javascript:alert(1)", "<p>javascript:alert(1)</p>"},
		{"link in code not linked", "`https://example.com`", "<p><code>https://example.com</code></p>"},
		{"emphasis around link", "**https://example.com**", `<p><strong><a href="https://example.com" rel="ugc nofollow noopener">https://example.com</a></strong></p>`},
		{"bare domain stays text", "example.com", "<p>example.com</p>"},
		{"quote ends the url", `https://example.com/"onmouseover="x`, `<p><a href="https://example.com/" rel="ugc nofollow noopener">https://example.com/</a>"onmouseover="x</p>`},
	}
	for _, c := range cases {
		res, err := Render(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if res.HTML != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, res.HTML, c.want)
		}
		if strings.Contains(res.HTML, "<a ") && !res.HasLinks {
			t.Errorf("%s: HTML has a link but HasLinks is false", c.name)
		}
		again, _ := Render(c.in)
		if again.HTML != res.HTML {
			t.Errorf("%s: not deterministic", c.name)
		}
	}
}

func TestRenderResult(t *testing.T) {
	t.Parallel()
	res, err := Render("see https://WWW.Example.com/a and example.org")
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasLinks || strings.Join(res.LinkDomains, ",") != "example.com,example.org" {
		t.Fatalf("got %+v", res)
	}
	if res.BodyHash != BodyHash("see https://WWW.Example.com/a and example.org") {
		t.Fatal("BodyHash mismatch")
	}
}

func TestRenderErrors(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", " \n\t ", "\x00\x01"} {
		if _, err := Render(in); !errors.Is(err, ErrEmpty) {
			t.Errorf("Render(%q) err = %v, want ErrEmpty", in, err)
		}
	}
	if _, err := Render(strings.Repeat("a", MaxBodyBytes)); err != nil {
		t.Errorf("max length: %v", err)
	}
	if _, err := Render(strings.Repeat("a", MaxBodyBytes+1)); !errors.Is(err, ErrTooLong) {
		t.Errorf("over max: err = %v, want ErrTooLong", err)
	}
}

func TestBodyHash(t *testing.T) {
	t.Parallel()
	if BodyHash("A  b\n") != BodyHash("a b") {
		t.Fatal("whitespace and case must not change the hash")
	}
	if BodyHash("a b") == BodyHash("a c") {
		t.Fatal("different bodies share a hash")
	}
}
