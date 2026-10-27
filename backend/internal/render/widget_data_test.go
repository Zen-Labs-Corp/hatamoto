package render

import (
	"bytes"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

func renderWithData(t *testing.T, ds DataSource, v *PageView, canvas bool) string {
	t.Helper()
	e, err := New(Options{Data: ds})
	if err != nil {
		t.Fatal(err)
	}
	v.Canvas = canvas
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	return mainInner(t, buf.String())
}

func dataDoc(t *testing.T, name string) *builder.Doc {
	t.Helper()
	return loadWidgetDoc(t, name)
}

func TestFormWidget(t *testing.T) {
	v := minimalView()
	v.Doc = dataDoc(t, "form")
	v.Forms = map[string]FormView{"contact": {Slug: "contact", Name: "Contact", SubmitLabel: "Go", Fields: []FormField{
		{Name: "email", Label: "Email <you>", Type: "email", Required: true},
		{Name: "msg", Label: "Message", Type: "textarea"},
		{Name: "topic", Label: "Topic", Type: "select", Options: []FormOption{{Value: "a", Label: "A"}}},
		{Name: "ok", Label: "Agree", Type: "checkbox"},
	}}}
	v.Submitted = true
	pub := renderWithData(t, nil, v, false)
	for _, want := range []string{
		`action="/forms/contact/submit"`, `name="hm_website"`, `name="_redirect" value="/fixture"`, `role="status"`,
		`type="email"`, `<textarea id="hm-form-contact-msg"`, `<select id="hm-form-contact-topic"`, `>Go</button>`, `Email &lt;you&gt;`,
	} {
		if !strings.Contains(pub, want) {
			t.Errorf("form: missing %q\n%s", want, pub)
		}
	}
	if strings.Contains(pub, "data-hm-") {
		t.Error("public form carries data-hm-*")
	}
	if can := renderWithData(t, nil, v, true); !strings.Contains(can, `data-hm-form="contact"`) {
		t.Error("canvas form lacks its marker")
	}
}

func TestFormWidgetUnknownSlug(t *testing.T) {
	v := minimalView()
	v.Doc = dataDoc(t, "form")
	if pub := renderWithData(t, nil, v, false); strings.Contains(pub, "hm-n-f1") || strings.Contains(pub, "<form") {
		t.Errorf("unknown form rendered publicly: %s", pub)
	}
	if can := renderWithData(t, nil, v, true); !strings.Contains(can, "Form not found") {
		t.Errorf("canvas shows no placeholder: %s", can)
	}
}

func TestMenuWidget(t *testing.T) {
	v := minimalView()
	v.Path = "/about"
	v.Doc = dataDoc(t, "menu")
	pub := renderWithData(t, fakeData{}, v, false)
	if !strings.Contains(pub, `<nav class="hm-n hm-menu hm-n-m1" aria-label="Menu"><ul class="hm-menu__l">`) || !strings.Contains(pub, `<a href="/primary">`) {
		t.Errorf("menu: %s", pub)
	}
	if pub = renderWithData(t, nil, v, false); strings.Contains(pub, "hm-n-m1") {
		t.Errorf("no data source, no menu: %s", pub)
	}
}

func TestBreadcrumbsWidget(t *testing.T) {
	v := minimalView()
	v.Doc = dataDoc(t, "breadcrumbs")
	v.Breadcrumb = []Crumb{{Name: "Home", URL: "/"}, {Name: "Blog <x>", URL: "/blog"}, {Name: "Post", URL: "/blog/post"}}
	pub := renderWithData(t, nil, v, false)
	want := `<nav class="hm-n hm-crumbs hm-n-b1" aria-label="Breadcrumb"><ol><li><a href="/">Home</a></li><li><a href="/blog">Blog &lt;x&gt;</a></li><li><span aria-current="page">Post</span></li></ol></nav>`
	if !strings.Contains(pub, want) {
		t.Errorf("breadcrumbs: %s", pub)
	}
	v.Breadcrumb = nil
	if pub = renderWithData(t, nil, v, false); strings.Contains(pub, "hm-n-b1") {
		t.Errorf("empty trail rendered publicly: %s", pub)
	}
	if can := renderWithData(t, nil, v, true); !strings.Contains(can, `aria-current="page">This page`) {
		t.Errorf("canvas placeholder trail missing: %s", can)
	}
}

// A form that requires the spam check carries the widget container before the
// submit button, in the widget and in the contact_form section; without it
// neither template emits anything (the goldens stay byte-identical).
func TestFormCaptchaMarkup(t *testing.T) {
	form := func(captcha bool) FormView {
		fv := FormView{Slug: "contact", Name: "Contact", SubmitLabel: "Go", Fields: []FormField{{Name: "email", Label: "Email", Type: "email"}}}
		if captcha {
			fv.Captcha, fv.CaptchaProvider, fv.CaptchaSiteKey = true, "turnstile", `0x"4AAA`
		}
		return fv
	}
	const container = `<div class="hm-form__captcha" data-hm-captcha="turnstile" data-sitekey="0x&#34;4AAA"></div><button`

	widget := func(captcha, canvas bool) string {
		v := minimalView()
		v.Doc = dataDoc(t, "form")
		v.Forms = map[string]FormView{"contact": form(captcha)}
		return renderWithData(t, nil, v, canvas)
	}
	section := func(captcha bool) string {
		e, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		v := minimalView()
		v.Forms = map[string]FormView{"contact": form(captcha)}
		v.Sections = []Section{{ID: "s", Type: "contact_form", Content: map[string]any{"form_slug": "contact"}}}
		var buf bytes.Buffer
		if err := e.RenderPage(&buf, v); err != nil {
			t.Fatal(err)
		}
		return mainInner(t, buf.String())
	}

	for name, got := range map[string]string{
		"widget public": widget(true, false), "widget canvas": widget(true, true), "section": section(true),
	} {
		if !strings.Contains(got, container) {
			t.Errorf("%s: missing captcha container before the button\n%s", name, got)
		}
		if strings.Contains(got, "secret") {
			t.Errorf("%s: output mentions a secret", name)
		}
	}
	for name, got := range map[string]string{
		"widget public": widget(false, false), "widget canvas": widget(false, true), "section": section(false),
	} {
		if strings.Contains(got, "captcha") {
			t.Errorf("%s: captcha markup without the switch\n%s", name, got)
		}
	}
}
