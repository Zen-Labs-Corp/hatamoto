package render

import (
	"strings"
	"testing"
)

type navData struct{ fakeData }

func (navData) Menu(string) []NavItem {
	return []NavItem{
		{Label: "Product", Href: "#", NoLink: true, Class: "hm-nav__item--muted", Badge: "New", BadgeClass: "hm-nav__badge--new"},
		{Label: "Start", Href: "/start", Class: "footer-cta hm-nav__item--button", Rel: "nofollow", ID: "go", Title: "Go", AriaLabel: "Get started", Icon: "arrow"},
	}
}

func renderShell(t *testing.T, v *PageView) string {
	t.Helper()
	e := newTestEngine(t, Options{Data: navData{}})
	var b strings.Builder
	if err := e.RenderPage(&b, v); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestDefaultThemeNoLinkRendersSpan(t *testing.T) {
	v := minimalView()
	v.Site.Nav = navData{}.Menu("header")
	v.Site.FooterNav = []NavItem{
		{Label: "Company", Children: navData{}.Menu("footer")},
		{Label: "Loose", Href: "#", NoLink: true},
	}
	out := renderShell(t, v)
	for _, want := range []string{
		`<li><span class="hm-nav__item--muted">Product <span class="hm-nav__badge hm-nav__badge--new">New</span></span></li>`,
		`<a href="/start" class="footer-cta hm-nav__item--button" id="go" title="Go" aria-label="Get started" data-icon="arrow" rel="nofollow">Start</a>`,
		`<li><span>Loose</span></li>`,
		`<p class="hm-footer__title">Company</p>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
	if strings.Contains(out, `href="#"`) {
		t.Errorf("a no-link item emitted href=\"#\":\n%s", out)
	}
}

// A header parent without a link must stay focusable, or :focus-within never
// opens its sub-menu for keyboard users.
func TestNoLinkParentIsFocusable(t *testing.T) {
	v := minimalView()
	v.Site.Nav = []NavItem{
		{Label: "Product", Href: "#", NoLink: true, Children: []NavItem{{Label: "Pricing", Href: "/pricing"}}},
		{Label: "Loose", Href: "#", NoLink: true},
	}
	out := renderShell(t, v)
	if want := `<li><span tabindex="0">Product</span><ul class="hm-nav__sub">`; !strings.Contains(out, want) {
		t.Errorf("missing %s in:\n%s", want, out)
	}
	if want := `<li><span>Loose</span></li>`; !strings.Contains(out, want) {
		t.Errorf("a childless no-link item must stay a plain span:\n%s", out)
	}
}

func TestNavLinkTargetKeepsRel(t *testing.T) {
	v := minimalView()
	v.Site.Nav = []NavItem{{Label: "Ext", Href: "https://example.com/", Target: "_blank", Rel: "nofollow"}}
	out := renderShell(t, v)
	if want := `<a href="https://example.com/" target="_blank" rel="noopener noreferrer nofollow">Ext</a>`; !strings.Contains(out, want) {
		t.Errorf("missing %s in:\n%s", want, out)
	}
}

func TestMenuWidgetCarriesClassesAndNoLink(t *testing.T) {
	v := minimalView()
	v.Doc = dataDoc(t, "menu")
	pub := renderWithData(t, navData{}, v, false)
	for _, want := range []string{
		`<li><span class="hm-nav__item--muted">Product <span class="hm-nav__badge hm-nav__badge--new">New</span></span></li>`,
		`<a href="/start" class="footer-cta hm-nav__item--button" id="go" title="Go" aria-label="Get started" data-icon="arrow" rel="nofollow">Start</a>`,
	} {
		if !strings.Contains(pub, want) {
			t.Errorf("widget missing %s in:\n%s", want, pub)
		}
	}
}
