package entity

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// unsafeMarkup reports what no sanitiser policy may let through: a script or
// form element, an on* handler, or a javascript: link or source.
func unsafeMarkup(out string) string {
	z := html.NewTokenizer(strings.NewReader(out))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			if tok.Data == "script" || tok.Data == "form" {
				return "<" + tok.Data + ">"
			}
			for _, a := range tok.Attr {
				k := strings.ToLower(a.Key)
				v := strings.ToLower(strings.Map(func(r rune) rune {
					if r <= ' ' {
						return -1
					}
					return r
				}, a.Val))
				if strings.HasPrefix(k, "on") {
					return "attribute " + a.Key
				}
				if (k == "href" || k == "src") && strings.HasPrefix(v, "javascript:") {
					return "javascript: in " + a.Key
				}
			}
		}
	}
}

// FuzzSanitizeHTML runs both HTML policies over arbitrary markup: the output
// must be free of script, forms, event handlers and javascript: URLs.
func FuzzSanitizeHTML(f *testing.F) {
	raw, err := os.ReadFile("testdata/rich-html-samples.json")
	if err != nil {
		f.Fatal(err)
	}
	var samples []struct {
		HTML string `json:"html"`
	}
	if err := json.Unmarshal(raw, &samples); err != nil {
		f.Fatal(err)
	}
	for _, s := range samples {
		f.Add(s.HTML)
	}
	for _, s := range []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`<a href="java&#9;script:alert(1)">x</a>`,
		`<form action="/x"><input name=a></form>`,
		`<svg><script>alert(1)</script></svg>`,
		`<iframe src="https://evil.test/"></iframe>`,
		`<div style="background:url(javascript:alert(1))">x</div>`,
		`<math><mi xlink:href="javascript:alert(1)">x</mi></math>`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		if bad := unsafeMarkup(SanitizeHTMLSectionBody(in)); bad != "" {
			t.Fatalf("html-section policy kept %s for %q", bad, in)
		}
		if bad := unsafeMarkup(SanitizeRichText(in)); bad != "" {
			t.Fatalf("rich-text policy kept %s for %q", bad, in)
		}
	})
}

// inlineViolation reports what the inline profile must never output: any
// element outside the allow-list, an attribute other than a's href, target
// and rel, an unsafe href, a target other than _blank or a _blank link
// without noopener.
func inlineViolation(out string) string {
	if bad := unsafeMarkup(out); bad != "" {
		return bad
	}
	allowed := map[string]bool{"strong": true, "em": true, "b": true, "i": true, "u": true, "s": true, "code": true, "br": true, "a": true}
	z := html.NewTokenizer(strings.NewReader(out))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			tok := z.Token()
			if !allowed[tok.Data] {
				return "element " + tok.Data
			}
			if tok.Type == html.EndTagToken {
				continue
			}
			var href, target, rel string
			for _, a := range tok.Attr {
				if tok.Data != "a" {
					return "attribute " + a.Key + " on " + tok.Data
				}
				switch a.Key {
				case "href":
					href = a.Val
				case "target":
					target = a.Val
				case "rel":
					rel = a.Val
				default:
					return "attribute " + a.Key
				}
			}
			if tok.Data != "a" {
				continue
			}
			if !AllowedInlineHref(href) {
				return "href " + href
			}
			if target != "" && target != "_blank" {
				return "target " + target
			}
			if target == "_blank" && !strings.Contains(rel, "noopener") {
				return "_blank without noopener"
			}
		}
	}
}

// FuzzSanitizeInline runs the inline profile over arbitrary markup: the output
// stays inside the allow-list, and sanitising it again changes nothing.
func FuzzSanitizeInline(f *testing.F) {
	raw, err := os.ReadFile("testdata/rich-html-samples.json")
	if err != nil {
		f.Fatal(err)
	}
	var samples []struct {
		HTML string `json:"html"`
	}
	if err := json.Unmarshal(raw, &samples); err != nil {
		f.Fatal(err)
	}
	for _, s := range samples {
		f.Add(s.HTML)
	}
	for _, s := range []string{
		"a&#13;b", "a\rb\r\nc",
		`<script>alert(1)</script>`,
		`<a href="java&#9;script:alert(1)" target=_blank rel=opener>x</a>`,
		`<a href="//evil.test/x" target="_blank">x</a>`,
		`<a href="/ok" target="_blank" onclick=x>y<strong>z`,
		`<em><a href=/x>a</em>b</a>`,
		`<svg><a xlink:href="javascript:x">a</a></svg>`,
		`<math><mi><a href="data:text/html,x">a</a></mi></math>`,
		`<a href="/x"><a href="/y">n</a></a>`,
		`<b><i><u>x</b></u></i><br></br>`,
		`<p>a</p><table><a href=/x>t</a></table>`,
		`<noscript><a href="/x" title="</noscript><img src=x onerror=alert(1)>">a</a></noscript>`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := SanitizeInlineHTML(in)
		if bad := inlineViolation(out); bad != "" {
			t.Fatalf("inline policy kept %s for %q -> %q", bad, in, out)
		}
		if again := SanitizeInlineHTML(out); again != out {
			t.Fatalf("not idempotent for %q: %q -> %q", in, out, again)
		}
	})
}
