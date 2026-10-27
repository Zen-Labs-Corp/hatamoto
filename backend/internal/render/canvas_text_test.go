package render

import (
	"errors"
	"strings"
	"testing"
)

func texts(t *testing.T, body string) []string {
	t.Helper()
	nodes, err := EditableTextNodes(body)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(nodes))
	for i, n := range nodes {
		if n.Index != i {
			t.Fatalf("index %d at position %d", n.Index, i)
		}
		out[i] = n.Text
	}
	return out
}

func TestEditableTextNodes(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
	}{
		{"nested inline", `<p>Hi <b>x</b> you</p>`, []string{"Hi ", "x", " you"}},
		{"heading", `<h1>T</h1>`, []string{"T"}},
		{"root text", `lead<p>a</p>`, []string{"lead", "a"}},
		{"button", `<button>Go</button>`, []string{"Go"}},
		{"entity", `<p>Can&#39;t &amp; won&rsquo;t</p>`, []string{"Can't & won’t"}},
		{"whitespace and nbsp", "<p>\n  </p><span>&nbsp;</span><p> a </p>", []string{" a "}},
		{"skip set", `<pre>p</pre><p>a<code>c</code>b</p><script>s</script><style>st</style><svg><text>v</text></svg><textarea>ta</textarea><noscript>n</noscript><template>t</template><select><option>o</option></select>`, []string{"a", "b"}},
		{"br separated", `a<br>b`, []string{"a", "b"}},
		{"unicode", `<p>日本語 😀</p>`, []string{"日本語 😀"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := texts(t, c.in)
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestStampEditableText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"mixed", `<p>Hi <b>x</b> you</p>`,
			`<p><span data-hm-text="body#0">Hi </span><b data-hm-text="body#1">x</b><span data-hm-text="body#2"> you</span></p>`},
		{"heading direct", `<h1>T</h1>`, `<h1 data-hm-text="body#0">T</h1>`},
		{"root text wrapped", `lead<p>a</p>`, `<span data-hm-text="body#0">lead</span><p data-hm-text="body#1">a</p>`},
		{"button wrapped", `<button>Go</button>`, `<button><span data-hm-text="body#0">Go</span></button>`},
		{"summary wrapped", `<details><summary>Q?</summary>a</details>`,
			`<details><summary><span data-hm-text="body#0">Q?</span></summary><span data-hm-text="body#1">a</span></details>`},
		{"skipped untouched", `<pre>p</pre><script>s</script>`, `<pre>p</pre><script>s</script>`},
		{"path", `<a href="/x">L</a>`, `<a href="/x" data-hm-text="items.1.b#0">L</a>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := "body"
			if c.name == "path" {
				path = "items.1.b"
			}
			if got := StampEditableText(c.in, path); got != c.want {
				t.Fatalf("got  %s\nwant %s", got, c.want)
			}
		})
	}
}

func TestReplaceTextNode(t *testing.T) {
	body := `<p>Hi <b>x</b> you</p>`
	got, err := ReplaceTextNode(body, 1, "x", "y")
	if err != nil || got != `<p>Hi <b>y</b> you</p>` {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := ReplaceTextNode(body, 1, "nope", "y"); !errors.Is(err, ErrStaleText) {
		t.Fatalf("stale: %v", err)
	}
	if _, err := ReplaceTextNode(body, 1, "x", " \u00a0"); !errors.Is(err, ErrBlank) {
		t.Fatalf("blank: %v", err)
	}
	for _, i := range []int{-1, 3, 99} {
		if _, err := ReplaceTextNode(body, i, "x", "y"); !errors.Is(err, ErrRange) {
			t.Fatalf("range %d: %v", i, err)
		}
	}
	got, err = ReplaceTextNode(body, 1, "x", "<script>alert(1)</script>")
	if err != nil || !strings.Contains(got, "&lt;script&gt;") || strings.Contains(got, "<script>") {
		t.Fatalf("escape: %q, %v", got, err)
	}
}
