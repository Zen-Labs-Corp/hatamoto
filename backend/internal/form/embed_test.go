package form

import (
	"strings"
	"testing"
)

func embedFixture() *Form {
	return &Form{
		Slug:     "contact",
		Settings: FormSettings{SubmitButtonText: "Go <now>"},
		Fields: []FormField{
			{Type: FieldTypeHeading, Text: "Hi & welcome"},
			{Type: FieldTypeText, Name: "name", Label: `Your "name"`, Required: true, Placeholder: "A<B"},
			{Type: FieldTypeEmail, Name: "email", Label: "Email"},
			{Type: FieldTypePhone, Name: "phone", Label: "Phone"},
			{Type: FieldTypeTextarea, Name: "msg", Label: "Message"},
			{Type: FieldTypeSelect, Name: "topic", Label: "Topic", Options: []FieldOption{{Label: "One", Value: "1"}}},
			{Type: FieldTypeRadio, Name: "pick", Label: "Pick", Required: true, Options: []FieldOption{{Label: "A", Value: "a"}}},
			{Type: FieldTypeCheckbox, Name: "multi", Label: "Multi", Options: []FieldOption{{Label: "X", Value: "x"}}},
			{Type: FieldTypeConsent, Name: "ok", Label: "I agree", Required: true, Link: "/privacy"},
			{Type: FieldTypeHidden, Name: "src", Hidden: &HiddenSpec{Source: "static", Value: "v&1"}},
			{Type: FieldTypeDivider},
		},
	}
}

func TestEmbedHTMLFields(t *testing.T) {
	out := EmbedHTML(embedFixture(), "https://example.com/forms/contact/submit")
	for _, want := range []string{
		`action="https://example.com/forms/contact/submit"`,
		`name="hm_website"`,
		`<h3>Hi &amp; welcome</h3>`,
		`Your &#34;name&#34;`,
		`placeholder="A&lt;B"`,
		`type="email"`,
		`type="tel"`,
		`<textarea id="hm-form-contact-msg" name="msg"`,
		`<option value="1">One</option>`,
		`type="radio" name="pick" value="a" required`,
		`type="checkbox" name="multi" value="x"`,
		`name="ok" value="Yes" required`,
		`<a href="/privacy"`,
		`type="hidden" name="src" value="v&amp;1"`,
		`<hr>`,
		`Go &lt;now&gt;`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "_redirect") || strings.Contains(out, "<script") {
		t.Errorf("unexpected markup:\n%s", out)
	}
}

func TestEmbedHTMLHoneypot(t *testing.T) {
	f := embedFixture()
	f.Settings.HoneypotOff = true
	if out := EmbedHTML(f, "/x"); strings.Contains(out, "hm_website") {
		t.Errorf("honeypot present when off:\n%s", out)
	}
}
