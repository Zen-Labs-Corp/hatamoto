package media

import (
	"errors"
	"strings"
	"testing"
)

const svgHead = `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 10 10">`

func TestSanitizeSVGCleanRoundTrip(t *testing.T) {
	in := `<?xml version="1.0"?>` + svgHead + `<!-- c --><defs><linearGradient id="g"><stop offset="0" stop-color="#fff"/></linearGradient></defs>` +
		`<path d="M0 0L10 10" fill="url(#g)" style="stroke:red"/><text x="1">a &amp; b</text><use xlink:href="#g"/></svg>`
	out, removed, err := SanitizeSVG([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Errorf("clean svg reported removals: %v", removed)
	}
	for _, want := range []string{`fill="url(#g)"`, `style="stroke:red"`, `a &amp; b`, `xlink:href="#g"`, `viewBox="0 0 10 10"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
	if strings.Contains(string(out), "<!--") {
		t.Error("comment kept")
	}
	// idempotent
	again, _, err := SanitizeSVG(out)
	if err != nil || string(again) != string(out) {
		t.Errorf("not idempotent: %v\n%s\n%s", err, out, again)
	}
}

func TestSanitizeSVGRemoves(t *testing.T) {
	cases := []struct {
		name, body string
		gone       []string
	}{
		{"script", `<script>alert(1)</script><rect width="1"/>`, []string{"script", "alert"}},
		{"foreignObject", `<foreignObject><div>x</div></foreignObject><rect width="1"/>`, []string{"foreignObject", "div"}},
		{"onload", `<rect width="1" onclick="x()" onload="y()"/>`, []string{"onclick", "onload"}},
		{"remote href", `<image href="https://evil.test/a.png"/><rect width="1"/>`, []string{"evil.test"}},
		{"use remote", `<use xlink:href="https://evil.test/a.svg#x"/><rect width="1"/>`, []string{"evil.test"}},
		{"javascript href", `<image href="javascript:alert(1)"/><rect width="1"/>`, []string{"javascript"}},
		{"import", `<style>@import url(http://evil.test/x.css); rect{fill:red}</style><rect width="1"/>`, []string{"@import", "evil.test"}},
		{"style url", `<rect width="1" style="background:url(http://evil.test/x)"/>`, []string{"evil.test"}},
		{"fill url", `<rect width="1" fill="url(http://evil.test/x)"/>`, []string{"evil.test"}},
		{"prefixed script", `<svg:script xmlns:svg="http://www.w3.org/2000/svg">alert(1)</svg:script><rect width="1"/>`, []string{"script", "alert"}},
		{"xhtml script", `<a xmlns="http://www.w3.org/1999/xhtml"><script>alert(1)</script></a><rect width="1"/>`, []string{"script", "alert"}},
		{"css escape", `<style>rect{fill:u\72l(http://evil.test)}</style><rect width="1"/>`, []string{"evil.test"}},
		{"stylesheet pi", `<?xml-stylesheet href="http://evil.test/x.css"?><rect width="1"/>`, []string{"evil.test"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, removed, err := SanitizeSVG([]byte(svgHead + c.body + `</svg>`))
			if err != nil {
				t.Fatal(err)
			}
			if len(removed) == 0 {
				t.Error("nothing reported as removed")
			}
			for _, g := range c.gone {
				if strings.Contains(string(out), g) {
					t.Errorf("%q survived: %s", g, out)
				}
			}
			if !strings.Contains(string(out), "<rect") {
				t.Errorf("rect lost: %s", out)
			}
		})
	}
}

func TestSanitizeSVGRefuses(t *testing.T) {
	deep := svgHead + strings.Repeat("<g>", 100) + strings.Repeat("</g>", 100) + "</svg>"
	big := svgHead + `<path d="` + strings.Repeat("M0 0", 150<<10) + `"/></svg>`
	cases := map[string]string{
		"doctype":     `<!DOCTYPE svg PUBLIC "x" "http://evil.test/x.dtd">` + svgHead + `<rect/></svg>`,
		"entity":      `<!DOCTYPE svg [<!ENTITY a "aaaa"><!ENTITY b "&a;&a;&a;">]>` + svgHead + `<rect/></svg>`,
		"deep":        deep,
		"big":         big,
		"malformed":   svgHead + `<rect></svg>`,
		"not svg":     `<html><body/></html>`,
		"empty":       ``,
		"nothing":     svgHead + `<script>x</script></svg>`,
		"two roots":   svgHead + `<rect/></svg><svg><rect/></svg>`,
		"text":        svgHead + `<rect/></svg>junk`,
		"unknown ent": svgHead + `<text>&nbsp;</text></svg>`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := SanitizeSVG([]byte(in))
			if !errors.Is(err, ErrInvalidSVG) {
				t.Fatalf("want ErrInvalidSVG, got %v", err)
			}
			var se *SVGError
			if !errors.As(err, &se) || se.Reason == "" {
				t.Fatalf("no reason: %v", err)
			}
		})
	}
	_, _, err := SanitizeSVG([]byte("<svg>\n<rect>\n</svg>"))
	var se *SVGError
	if !errors.As(err, &se) || se.Line != 3 {
		t.Errorf("line = %+v, want 3", se)
	}
}
