package render

import (
	"bytes"
	"encoding/json/jsontext"
	"net/http"
	"os"
	"strings"
	"testing"
)

func emailFixture() *PageView {
	v := minimalView()
	v.JSONLD = []jsontext.Value{[]byte(`{"@context":"https://schema.org","email":"ld@example.org"}`)}
	v.Site.HeaderCTA = &Link{Label: "Write", URL: "mailto:cta@example.org"}
	v.Site.FooterNav = []NavItem{{Label: "Mail", Href: "mailto:menu@example.org"}}
	v.Sections = []Section{
		{ID: "r", Type: "content", Content: map[string]any{"body": `<p>Mail <a href="mailto:rich@example.org">rich@example.org</a></p>`}},
		{ID: "h", Type: "html", Content: map[string]any{"body": `<p>raw@example.org</p>`}},
		{ID: "c", Type: "contact_info", Content: map[string]any{"email": "info@example.org"}},
	}
	return v
}

var emailSources = []string{"rich@example.org", "raw@example.org", "info@example.org", "menu@example.org", "cta@example.org"}

func TestEngineProtectsEmailSources(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	out := renderToString(t, e, emailFixture())
	body := out[strings.Index(out, "<body"):]
	if strings.Contains(strings.ToLower(body), "mailto:") {
		t.Errorf("literal mailto: in body")
	}
	for _, a := range emailSources {
		if strings.Contains(out, a) {
			t.Errorf("%s still literal", a)
		}
	}
	if !strings.Contains(out, `"email":"ld@example.org"`) {
		t.Error("JSON-LD address must stay plain")
	}
	if strings.Contains(out, "<script>") && strings.Count(out, "<script") != strings.Count(renderToString(t, e, minimalView()), "<script") {
		t.Error("script added to the page")
	}
}

func TestEngineEmailPlainAndEditorFramesLiteral(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	for name, mut := range map[string]func(*PageView){
		"plain":   func(v *PageView) { v.Site.EmailPlain = true },
		"preview": func(v *PageView) { v.Preview = true },
		"canvas":  func(v *PageView) { v.Canvas = true },
	} {
		v := emailFixture()
		mut(v)
		var b bytes.Buffer
		if err := e.RenderPage(&b, v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(b.String(), "mailto:menu@example.org") {
			t.Errorf("%s: output must stay literal", name)
		}
	}
}

// Coming-soon, maintenance and password notices are public pages, so the
// owner's message is protected like any other page; EmailPlain still opts out.
func TestEngineProtectsAccessNotice(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	for _, plain := range []bool{false, true} {
		v := emailFixture()
		v.Site.EmailPlain = plain
		v.Access = &AccessView{Mode: "coming_soon", Message: "Questions? hello@shop.example"}
		var b bytes.Buffer
		if err := e.RenderAccess(&b, v, http.StatusServiceUnavailable); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(b.String(), "hello@shop.example"); got != plain {
			t.Errorf("plain=%v: literal address present=%v\n%s", plain, got, b.String())
		}
	}
}

func TestEngineProtectsErrorAndNotFound(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t, Options{})
	var b bytes.Buffer
	if err := e.RenderNotFound(&b, emailFixture()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "menu@example.org") {
		t.Error("404 page keeps a literal address")
	}
}

// The docs must not promise more than the feature does: the organisation
// email in JSON-LD and the feeds stay plain.
func TestEmailProtectDocsStateLimits(t *testing.T) {
	t.Parallel()
	for _, f := range []string{"docs/site/themes/engine-details.md", "docs/RELEASE_NOTES.md", "docs/site/admin/settings-users-email.md"} {
		b, err := os.ReadFile("../../../" + f)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if !strings.Contains(s, "JSON-LD") && !strings.Contains(s, "structured data") {
			t.Errorf("%s: no word on structured data staying plain", f)
		}
		if strings.Contains(s, "finds nothing") {
			t.Errorf("%s: overclaims", f)
		}
	}
}
