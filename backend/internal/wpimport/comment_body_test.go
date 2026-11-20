package wpimport

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/commentfmt"
)

func TestCommentHTMLToText(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"empty", "", ""},
		{"plain", "Hello", "Hello"},
		{"paragraphs", "<p>One</p>\n\n<p>Two</p>", "One\n\nTwo"},
		{"bare blank line", "One\n\n\n\nTwo", "One\n\nTwo"},
		{"inline", "<strong>b</strong> <b>c</b> <em>i</em> <i>j</i> <code>x</code>", "**b** **c** *i* *j* `x`"},
		{"nested", "<p><strong>a <em>b</em></strong></p>", "**a *b***"},
		{"br", "a<br>b<br/>c", "a\nb\nc"},
		{"entities", "Tom &amp; Jerry&#8217;s &lt;b&gt;", "Tom & Jerry\u2019s <b>"},
		{"quote", "<blockquote>Said\n\nthis</blockquote>after", "> Said\n>\n> this\n\nafter"},
		{"link", `<a href="https://example.test/x">x</a>`, "x (https://example.test/x)"},
		{"link same text", `<a href="https://example.test/x">https://example.test/x</a>`, "https://example.test/x"},
		{"link empty label", `<a href="https://example.test/x"></a>`, "https://example.test/x"},
		{"mailto", `<a href="mailto:a@example.test">mail me</a>`, "mail me"},
		{"javascript", `<a href="javascript:alert(1)">click</a>`, "click"},
		{"data", `<a href="data:text/html,x">d</a>`, "d"},
		{"script", "a<script>alert(1)</script>b", "ab"},
		{"style iframe", "a<style>p{}</style><iframe src=\"https://example.test\">x</iframe>b", "ab"},
		{"unclosed", "<p><strong>bold <em>it", "**bold *it***"},
		{"stray close", "a</strong></a></blockquote>b", "ab"},
		{"unclosed quote", "<blockquote>q", "> q"},
		{"indent", "a\n    b\n\tc", "a\nb\nc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := commentHTMLToText(tc.in)
			if got != tc.want {
				t.Errorf("commentHTMLToText(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.Contains(got, "<script") || strings.Contains(got, "alert(1)") && strings.Contains(tc.in, "<script") {
				t.Errorf("script survived: %q", got)
			}
		})
	}
}

func TestCommentHTMLRoundTrip(t *testing.T) {
	res, err := commentfmt.Render(commentHTMLToText(`<p>See <a href="https://example.test/x">this</a> <strong>now</strong></p>`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasLinks {
		t.Error("HasLinks = false for a comment with a link")
	}
	if !strings.Contains(res.HTML, "<strong>now</strong>") || strings.Contains(res.HTML, "javascript") {
		t.Errorf("html = %s", res.HTML)
	}
	res, err = commentfmt.Render(commentHTMLToText(`<a href="javascript:alert(1)">x</a>`))
	if err != nil || res.HasLinks {
		t.Errorf("javascript link: %+v, %v", res, err)
	}
	if _, err := commentfmt.Render(commentHTMLToText("<p></p>")); err != commentfmt.ErrEmpty {
		t.Errorf("empty body err = %v", err)
	}
}
