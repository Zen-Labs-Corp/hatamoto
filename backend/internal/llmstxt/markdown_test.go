package llmstxt

import "testing"

func TestHTMLToMarkdown(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"headings", "<h1>One</h1><h3> Three  <em>x</em></h3>", "# One\n\n### Three _x_"},
		{"paragraphs and br", "<p>Hello\n  world</p><p>a<br>b</p>", "Hello world\n\na\nb"},
		{"link", `<p>See <a href="https://acme.example/a">our <strong>work</strong></a>.</p>`, "See [our **work**](https://acme.example/a)."},
		{"link without href", `<p><a name="x">Plain</a></p>`, "Plain"},
		{"image", `<p><img src="https://acme.example/i.png" alt="Office  view"></p>`, "![Office view](https://acme.example/i.png)"},
		{"image no src", `<p>a<img alt="x">b</p>`, "ab"},
		{"bullets nested", "<ul><li>One<ul><li>Inner</li></ul></li><li>Two</li></ul>", "- One\n  - Inner\n- Two"},
		{"ordered", "<ol><li>A</li><li>B</li></ol>", "1. A\n2. B"},
		{"pre", "<pre><code>a := 1\n  b</code></pre>", "```\na := 1\n  b\n```"},
		{"inline code", "<p>Run <code>make</code> now</p>", "Run `make` now"},
		{"blockquote", "<blockquote><p>Quote</p><p>Two</p></blockquote>", "> Quote\n>\n> Two"},
		{"table", "<table><thead><tr><th>Plan</th><th>Price</th></tr></thead><tbody><tr><td>Basic</td><td>10</td></tr></tbody></table>", "Plan | Price\nBasic | 10"},
		{"dropped", `<p>Keep</p><script>alert(1)</script><style>p{}</style><iframe src="x"></iframe><form><input></form>`, "Keep"},
		{"entities", "<p>Fish &amp; chips</p>", "Fish & chips"},
		{"div soup", "<div><div>One</div><div>Two</div></div>", "One\n\nTwo"},
		{"empty", "  ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HTMLToMarkdown(c.in); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestHTMLToMarkdownLinkSafety(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"mixed case", `<p><a href="JaVaScRiPt:alert(1)">go</a></p>`, "go"},
		{"tab inside scheme", "<p><a href=\"java&#9;script:alert(1)\">go</a></p>", "go"},
		{"leading control", "<p><a href=\"&#1; javascript:x\">go</a></p>", "go"},
		{"vbscript", `<p><a href="vbscript:x">go</a></p>`, "go"},
		{"data link", `<p><a href="data:text/html,hi">go</a></p>`, "go"},
		{"data image", `<p><img src="DATA:image/png;base64,AAAA" alt="x"></p>`, ""},
		{"javascript image", `<p><img src="javascript:x" alt="x"></p>`, ""},
		{"safe", `<p><a href="https://acme.example/a">go</a> <a href="/rel">r</a> <a href="mailto:a@acme.example">m</a></p>`, "[go](https://acme.example/a) [r](/rel) [m](mailto:a@acme.example)"},
		{"paren and space", `<p><a href="https://acme.example/a b)c(d">go</a></p>`, "[go](https://acme.example/a%20b%29c%28d)"},
		{"newline", "<p><a href=\"https://acme.example/a&#10;## injected\">go</a></p>", "[go](https://acme.example/a%0A##%20injected)"},
		{"alt brackets", `<p><img src="/i.png" alt="a](https://evil.example) [b"></p>`, `![a\](https://evil.example) \[b](/i.png)`},
	} {
		if got := HTMLToMarkdown(tc.in); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
