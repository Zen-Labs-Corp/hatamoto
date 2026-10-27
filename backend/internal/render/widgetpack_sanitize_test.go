package render

import (
	"io"

	"hatamoto.dev/core/internal/builder"
	"strings"
	"testing"
)

func TestSanitizePackOutputStripsHostileMarkup(t *testing.T) {
	for name, tc := range map[string]struct{ in, banned string }{
		"target":       {`<a href="https://e/" target="_blank" rel="opener">x</a>`, "target"},
		"script":       {`<script>alert(1)</script>`, "script"},
		"onerror":      {`<img src="/a.png" onerror="alert(1)">`, "onerror"},
		"onclick":      {`<p onclick="x()">t</p>`, "onclick"},
		"style attr":   {`<p style="position:fixed">t</p>`, "style"},
		"style el":     {`<style>*{}</style>`, "style"},
		"iframe":       {`<iframe src="https://e/"></iframe>`, "iframe"},
		"srcdoc":       {`<div srcdoc="x">t</div>`, "srcdoc"},
		"meta":         {`<meta http-equiv="refresh" content="0;url=https://e/">`, "meta"},
		"object":       {`<object data="x"></object><embed src="x">`, "embed"},
		"base":         {`<base href="https://e/">`, "base"},
		"link":         {`<link rel="stylesheet" href="https://e/">`, "link"},
		"form":         {`<form action="/x"><button formaction="https://e/">go</button></form>`, "formaction"},
		"js url":       {`<a href="javascript:alert(1)">x</a>`, "javascript"},
		"js url ws":    {`<a href=" JaVa&#9;script:alert(1)">x</a>`, "script:"},
		"data url":     {`<img src="data:text/html,x">`, "data:"},
		"vbscript":     {`<a href="vbscript:x">x</a>`, "vbscript"},
		"svg":          {`<svg onload="x()"><use href="#a"></use></svg>`, "svg"},
		"comment":      {`<!-- x --><b>t</b>`, "<!--"},
		"forged hm":    {`<div data-hm-id="other">t</div>`, "data-hm-"},
		"forged upper": {`<div DATA-HM-ID="other">t</div>`, "data-hm-"},
	} {
		t.Run(name, func(t *testing.T) {
			out, changed, err := sanitizePackOutput([]byte(`<div `+packAttrsHolder+`>`+tc.in+`</div>`), "", "")
			if err != nil {
				t.Fatal(err)
			}
			if !changed {
				t.Error("change not reported")
			}
			if s := strings.ToLower(string(out)); strings.Contains(s, tc.banned) {
				t.Errorf("%q survived: %s", tc.banned, out)
			}
		})
	}
}

func TestSanitizePackOutputKeepsSafeMarkup(t *testing.T) {
	in := `<div class="a" id="x" role="note" data-x="1" aria-label="l" title="t"><a href="https://e.com/p?q=1&amp;r=2" rel="noopener">l</a>` +
		`<a href="/rel">r</a><a href="#frag">f</a><a href="mailto:a@b.c">m</a><a href="tel:+1">t</a>` +
		`<img src="/media/a.jpg" alt="a" width="10" height="5" loading="lazy"><ul><li>x &lt;b&gt;</li></ul></div>`
	in = strings.Replace(in, `loading="lazy">`, `loading="lazy"/>`, 1) // the re-render writes void elements this way
	out, changed, err := sanitizePackOutput([]byte(in), "", "")
	if err != nil || changed || string(out) != in {
		t.Errorf("safe markup changed=%v err=%v:\n in: %s\nout: %s", changed, err, in, out)
	}
}

func TestSanitizePackOutputSplicesCore(t *testing.T) {
	attrs := `class="hm-n hm-promo_banner hm-n-p" data-hm-id="p" data-hm-t="promo_banner"`
	children := `<div data-hm-id="c" onclick="core()"><script>core()</script></div>`
	in := `<div ` + packAttrsHolder + `><b data-hm-id="forged">x</b>` + packChildrenHolder + `</div>`
	out, changed, err := sanitizePackOutput([]byte(in), attrs, children)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	want := `<div ` + attrs + `><b>x</b>` + children + `</div>`
	if string(out) != want {
		t.Errorf("out = %s\nwant %s", out, want)
	}
}

func TestSanitizePackOutputGuessedPlaceholdersDoNotSplice(t *testing.T) {
	out, _, err := sanitizePackOutput([]byte(`<div hma0="" ><hmc0></hmc0></div>`), `onclick="x"`, "<script>x</script>")
	if err != nil || strings.Contains(string(out), "onclick") || strings.Contains(string(out), "script") {
		t.Errorf("out = %s err=%v", out, err)
	}
}

func pagePack72() *builder.Doc {
	d := pageStat72()
	var walk func(ns []*builder.Node)
	walk = func(ns []*builder.Node) {
		for _, n := range ns {
			if len(n.C) > 0 {
				walk(n.C)
				continue
			}
			n.T, n.P = packKind, map[string]any{"title": "Title " + n.ID}
		}
	}
	walk(d.Nodes)
	return d
}

// BenchmarkRenderPagePack72 is BenchmarkRenderPageStat72 with 72 sanitised
// plugin widgets in place of the core stat widgets.
func BenchmarkRenderPagePack72(b *testing.B) {
	w, err := builder.ParsePackWidget("promo", "banner", []byte(packSchema), nil)
	if err != nil {
		b.Fatal(err)
	}
	if err := builder.SetPackKinds([]builder.PackWidgetSchema{w}); err != nil {
		b.Fatal(err)
	}
	if err := SetPackWidgets([]PackWidget{{Kind: packKind, HTML: []byte(packHTML), CSS: []byte(packCSS)}}); err != nil {
		b.Fatal(err)
	}
	defer func() { builder.SetPackKinds(nil); SetPackWidgets(nil) }()
	e, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	v := minimalView()
	v.Doc = pagePack72()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := e.RenderPage(io.Discard, v); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPackSanitize is the sanitising step alone, for one small widget.
func BenchmarkPackSanitize(b *testing.B) {
	in := []byte(`<div ` + packAttrsHolder + `><h3>Title</h3>` + packChildrenHolder + `</div>`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, _, err := sanitizePackOutput(in, `class="hm-n hm-promo_banner hm-n-p"`, `<p>child</p>`); err != nil {
			b.Fatal(err)
		}
	}
}

// Markup that bluemonday passes token by token but that would leak out of the
// widget box must come out balanced: nothing open, no stray end tag.
func TestSanitizePackOutputBalances(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"unclosed a":      {`<div ` + packAttrsHolder + `><a href="https://e.example/">click</div>`, `<div ><a href="https://e.example/">click</a></div>`},
		"unclosed select": {`<div ` + packAttrsHolder + `><select><option>x</div>`, `<div ><select><option>x</option></select></div>`},
		"stray main/head": {`<div ` + packAttrsHolder + `>x</main><header>fake</header></div>`, `<div >xfake</div>`},
		"unclosed table":  {`<div ` + packAttrsHolder + `><table><tr><td>x</div>`, `<div ><table><tbody><tr><td>x</td></tr></tbody></table></div>`},
	} {
		t.Run(name, func(t *testing.T) {
			out, _, err := sanitizePackOutput([]byte(tc.in), "", "")
			if err != nil || string(out) != tc.want {
				t.Errorf("out = %s err=%v\nwant %s", out, err, tc.want)
			}
		})
	}
}

// End to end: a widget that leaves an <a> open must not turn the sibling
// widget after it into a link.
func TestPackUnclosedLinkDoesNotLeak(t *testing.T) {
	// The validator refuses this template; the sanitiser is the second wall, so
	// call it directly.
	out, _, err := sanitizePackOutput([]byte(`<div `+packAttrsHolder+`><a href="https://e.example/">click</div>`), `class="x"`, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out) + `<p>sibling</p>`; strings.Count(got, "<a") != strings.Count(got, "</a>") {
		t.Errorf("unbalanced: %s", got)
	}
	err = ValidatePackWidget(packTestKind, PackFiles{HTML: []byte(`<div {{.Attrs}}><a href="https://e.example/">click</div>`)})
	if err == nil || !strings.Contains(err.Error(), "unbalanced-markup") {
		t.Errorf("validator err = %v", err)
	}
}
