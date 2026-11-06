package render

import (
	"bytes"
	"strings"
	"testing"
)

// The legacy contact_form section renders the layout, hidden and consent
// types as such, never as a text input.
func TestContactFormSectionNewTypes(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.Forms = map[string]FormView{"contact": {Slug: "contact", Name: "C", Fields: []FormField{
		{Name: "a", Label: "A", Type: "text"},
		{Type: "heading", Text: "Head <b>"},
		{Type: "paragraph", Text: "Para"},
		{Type: "divider"},
		{Name: "src", Type: "hidden", Hidden: true, HiddenValue: "v1"},
		{Name: "ok", Label: "I agree", Type: "consent", Required: true, Link: "/privacy"},
		{Name: "bad", Label: "Bad", Type: "consent", Link: "javascript:alert(1)"},
	}}}
	v.Sections = []Section{{ID: "s", Type: "contact_form", Content: map[string]any{"form_slug": "contact"}}}
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	out := mainInner(t, buf.String())
	for _, want := range []string{
		`<h3 class="hm-form__heading">Head &lt;b&gt;</h3>`, `<p class="hm-form__text">Para</p>`, `<hr class="hm-form__divider">`,
		`<input type="hidden" name="src" value="v1">`,
		`<label class="hm-form__consent"><input type="checkbox" id="hm-form-contact-ok" name="ok" value="Yes" required> <span><a href="/privacy" rel="noopener">I agree</a>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n%s", want, out)
		}
	}
	for _, bad := range []string{"javascript:", `name="src" type=`, `id="hm-form-contact-">`} {
		if strings.Contains(out, bad) {
			t.Errorf("unexpected %q", bad)
		}
	}
}
