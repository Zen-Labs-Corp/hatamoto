package render

import (
	"os"
	"regexp"
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
	if want := `<li><span tabindex="0" role="button">Product</span><ul class="hm-nav__sub">`; !strings.Contains(out, want) {
		t.Errorf("missing %s in:\n%s", want, out)
	}
	if strings.Contains(out, "aria-haspopup") || strings.Contains(out, "aria-expanded") {
		t.Errorf("the sub-list is plain links and the JS-off state is not known: no haspopup, no static expanded:\n%s", out)
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

// aria-expanded must re-sync for events that start inside the sub-list, or it
// stays "true" after focus or the pointer leaves through a sub-link.
func TestNavDisclosureSyncsFromAnywhereInTheList(t *testing.T) {
	b, err := os.ReadFile("themes/default/assets/js/hm.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	if !strings.Contains(js, "closest('.hm-nav__list')) setTimeout(syncParents") || strings.Contains(js, ":scope > [aria-expanded]") {
		t.Error("hm.js must call syncParents for any event inside .hm-nav__list")
	}
}

// A button-style item with a colour class keeps readable text: the colour fills
// the button and the label uses the page background, in light and dark.
func TestButtonColourContrast(t *testing.T) {
	raw, err := os.ReadFile("themes/default/assets/css/base.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	body := func(re string) string {
		m := regexp.MustCompile(re).FindStringSubmatch(css)
		if m == nil {
			t.Fatalf("block %q not found", re)
		}
		return m[1]
	}
	light := body(`(?s):root \{(.*?)\n\}`)
	dark := body(`(?s):root\[data-theme="dark"\] \{(.*?)\n\}`)
	decl := func(block, name string) string {
		m := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `:\s*([^;]+);`).FindStringSubmatch(block)
		if m == nil {
			return ""
		}
		return strings.TrimSpace(m[1])
	}
	// resolve follows var() chains; the dark block overrides the light one.
	var resolve func(blocks []string, name string) string
	resolve = func(blocks []string, name string) string {
		for _, b := range blocks {
			v := decl(b, name)
			if v == "" {
				continue
			}
			if m := regexp.MustCompile(`^var\((--[a-z-]+)\)$`).FindStringSubmatch(v); m != nil {
				return resolve(blocks, m[1])
			}
			return v
		}
		t.Fatalf("%s is not defined", name)
		return ""
	}
	// The colour rules come after the plain button's hover and match it in specificity, so a coloured button must keep a hover of its own.
	if !strings.Contains(css, `.hm-nav__item--button[class*="hm-nav-color--"]:hover { filter: brightness(0.9); }`) {
		t.Error("a coloured button has no hover rule")
	}
	for _, colour := range []string{"accent", "ink", "muted", "danger"} {
		re := `:is\(a, span, p\)\.hm-nav__item--button\.hm-nav-color--` + colour + ` \{ background: var\((--[a-z-]+)\); color: (var\(--[a-z-]+\)|#[0-9a-f]{6}); \}`
		m := regexp.MustCompile(re).FindStringSubmatch(css)
		if m == nil {
			t.Fatalf("no button rule for colour %s", colour)
		}
		for mode, blocks := range map[string][]string{"light": {light}, "dark": {dark, light}} {
			bg, fg := resolve(blocks, m[1]), m[2]
			if !strings.HasPrefix(fg, "#") {
				fg = resolve(blocks, strings.TrimSuffix(strings.TrimPrefix(fg, "var("), ")"))
			}
			if r := contrastRatio(t, fg, bg); r < 4.5 {
				t.Errorf("%s/%s: %s on %s = %.2f:1, want >= 4.5", mode, colour, fg, bg, r)
			}
		}
	}
}
