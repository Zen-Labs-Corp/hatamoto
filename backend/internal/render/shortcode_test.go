package render

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

func shortcodeView() *PageView {
	v := minimalView()
	v.Forms = map[string]FormView{"contact": {
		Slug: "contact", Name: "Contact", SubmitLabel: "Send",
		Fields: []FormField{{Name: "email", Label: "Email", Type: "email", Required: true}},
	}}
	return v
}

// TipTap-shaped text: the paragraph wraps the call and quotes arrive escaped.
const shortcodeText = `<p>Before</p><p>[form slug="contact"]</p><p>After</p>`

func assertFormExpanded(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, `action="/forms/contact/submit"`) || !strings.Contains(out, `name="email"`) {
		t.Fatalf("form markup missing:\n%s", out)
	}
	if strings.Contains(out, "[form") {
		t.Errorf("shortcode left in output")
	}
	if strings.Contains(out, "<p><div") || strings.Contains(out, "<p>\n<div") || strings.Contains(out, `<p><form`) || strings.Contains(out, `<p><div class="hm-n hm-formw`) {
		t.Errorf("form wrapped in <p>")
	}
	if !strings.Contains(out, "<p>Before</p>") || !strings.Contains(out, "<p>After</p>") {
		t.Errorf("surrounding text lost")
	}
}

func TestShortcodeRichtextWidget(t *testing.T) {
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "r", T: "richtext", P: map[string]any{"html": shortcodeText}}}}
	out := renderDocView(t, shortcodeView(), d, false)
	assertFormExpanded(t, out)
	if !strings.Contains(out, `hm-n-sc-contact`) {
		t.Errorf("widget attrs missing")
	}
}

func TestShortcodeRichtextSchemaField(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	v := shortcodeView()
	v.Sections = []Section{{ID: "c", Type: "content", Content: map[string]any{"body": shortcodeText}}}
	assertFormExpanded(t, renderToString(t, e, v))
}

func TestShortcodeBodyAttr(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	v := shortcodeView()
	v.Entity = &EntityView{}
	v.Attrs = map[string]any{"content": shortcodeText}
	assertFormExpanded(t, renderToString(t, e, v))
}

func TestShortcodeFormIslandAndCSSLoaded(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	v := shortcodeView()
	v.Sections = []Section{{ID: "c", Type: "content", Content: map[string]any{"body": shortcodeText}}}
	if out := renderToString(t, e, v); !v.formIsland || !strings.Contains(out, "hm-form") {
		t.Errorf("form island not requested (formIsland=%v)", v.formIsland)
	}
}

func TestShortcodeNotPublicRendersNothing(t *testing.T) {
	for _, slug := range []string{"paused", "nope"} {
		d := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "r", T: "richtext", P: map[string]any{"html": `<p>Keep</p><p>[form slug="` + slug + `"]</p>`}}}}
		out := renderDocView(t, shortcodeView(), d, false)
		if strings.Contains(out, "<form") || strings.Contains(out, "[form") || strings.Contains(out, "Form not found") {
			t.Errorf("slug %q rendered something:\n%s", slug, out)
		}
		if !strings.Contains(out, "<p>Keep</p>") {
			t.Errorf("slug %q: text lost", slug)
		}
	}
}

func TestShortcodeCanvasPlaceholder(t *testing.T) {
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "r", T: "richtext", P: map[string]any{"html": `<p>[form slug="nope"]</p>`}}}}
	if out := renderDocView(t, shortcodeView(), d, true); !strings.Contains(out, "Form not found") {
		t.Errorf("canvas shows no placeholder")
	}
}

func TestShortcodeFormMarkupBypassesSanitiser(t *testing.T) {
	// The policy strips <form>; the expansion must therefore come after it.
	if got := sanitisedHTML(`<p>[form slug="contact"]</p>`); strings.Contains(string(got), "<form") {
		t.Fatalf("plain sanitisedHTML must not expand: %s", got)
	}
	got := string(sanitisedHTML(`<p>[form slug="contact"]</p>`, shortcodeView()))
	if !strings.Contains(got, "<form") || !strings.Contains(got, `<input`) {
		t.Errorf("view-bound sanitisedHTML lost the form: %s", got)
	}
}

func TestShortcodeSanitiserStillStripsScripts(t *testing.T) {
	got := string(sanitisedHTML(`<p>x</p><script>alert(1)</script>[form slug="contact"]`, shortcodeView()))
	if strings.Contains(got, "<script") {
		t.Errorf("script survived: %s", got)
	}
}
