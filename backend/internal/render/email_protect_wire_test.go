package render

import (
	"bytes"
	"encoding/json/jsontext"
	"net/http"
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
		"access":  func(v *PageView) { v.Access = &AccessView{} },
	} {
		v := emailFixture()
		mut(v)
		var b bytes.Buffer
		var err error
		if name == "access" {
			err = e.RenderAccess(&b, v, http.StatusForbidden)
		} else {
			err = e.RenderPage(&b, v)
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if name != "access" && !strings.Contains(b.String(), "mailto:menu@example.org") {
			t.Errorf("%s: output must stay literal", name)
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
