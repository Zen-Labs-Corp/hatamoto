package render

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

var floatLinkRe = regexp.MustCompile(`<a class="hm-contact-float[^>]*>[^<]*</a>`)

func floatView(mut func(v *PageView)) *PageView {
	v := minimalView()
	v.Contact = ContactView{Phone: "+41 79 123 45 67", Email: "shop@example.org"}
	v.Site.ContactFloat = ContactFloat{Enabled: true, Channel: "whatsapp", Position: "bottom-right", Message: "Hello"}
	if mut != nil {
		mut(v)
	}
	return v
}

func renderFloatPage(t *testing.T, e *Engine, v *PageView) string {
	t.Helper()
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestContactFloatGolden(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		mut  func(v *PageView)
	}{
		{"enabled", nil},
		{"left call label", func(v *PageView) {
			v.Site.ContactFloat = ContactFloat{Enabled: true, Channel: "call", Position: "bottom-left", Label: "Call us <now>"}
		}},
		{"email own address", func(v *PageView) {
			v.Site.ContactFloat = ContactFloat{Enabled: true, Channel: "email", To: "own@example.org", Position: "bottom-right"}
		}},
		{"disabled", func(v *PageView) { v.Site.ContactFloat.Enabled = false }},
		{"preview", func(v *PageView) { v.Preview = true }},
		{"no target", func(v *PageView) { v.Contact = ContactView{} }},
	}
	var out strings.Builder
	for _, c := range cases {
		page := renderFloatPage(t, e, floatView(c.mut))
		links := floatLinkRe.FindAllString(page, -1)
		out.WriteString("## " + c.name + "\n" + strings.Join(links, "\n") + "\n")
		want := 1
		switch c.name {
		case "disabled", "preview", "no target":
			want = 0
		}
		if len(links) != want || strings.Count(page, `class="hm-contact-float`) != want {
			t.Errorf("%s: %d float links", c.name, len(links))
		}
	}
	compareGolden(t, "contact-float", out.String())
}

// The error, not-found and access pages carry no floating link even when it is enabled.
func TestContactFloatNotOnSpecialPages(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := e.RenderNotFound(&buf, floatView(nil)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "hm-contact-float") {
		t.Error("404 has the floating link")
	}
	buf.Reset()
	if err := e.RenderError(&buf, floatView(nil), 500); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "hm-contact-float") {
		t.Error("error page has the floating link")
	}
	buf.Reset()
	v := floatView(func(v *PageView) { v.Access = &AccessView{} })
	if err := e.RenderAccess(&buf, v, 503); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "hm-contact-float") {
		t.Error("access notice has the floating link")
	}
}

func TestContactFloatPageHasNoInlineScript(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	page := renderFloatPage(t, e, floatView(nil))
	if !strings.Contains(page, "hm-contact-float") {
		t.Fatal("no floating link")
	}
	for _, is := range inlineScriptIssues(page) {
		t.Error(is)
	}
	if m := floatLinkRe.FindString(page); strings.Contains(m, "style=") || strings.Contains(m, "<script") {
		t.Errorf("float markup: %s", m)
	}
}

func TestContactFloatCSSInBundle(t *testing.T) {
	th, err := LoadTheme(Chain{DefaultFS()})
	if err != nil {
		t.Fatal(err)
	}
	css, _, _ := buildBundle(th)
	s := string(css)
	for _, want := range []string{"env(safe-area-inset-bottom)", "env(safe-area-inset-right)", "env(safe-area-inset-left)", "@media print"} {
		if !strings.Contains(s, want) {
			t.Errorf("bundle lacks %q", want)
		}
	}
	i := strings.Index(s, ".hm-contact-float")
	if i < 0 || !strings.Contains(s[i:], "@media print{.hm-contact-float{display:none") && !strings.Contains(s[i:], "@media print { .hm-contact-float { display: none") {
		t.Error("print rule for the floating link missing")
	}
}
