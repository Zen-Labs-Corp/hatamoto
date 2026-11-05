package admin

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/siteconfig"
)

const brandingURL = "/admin/settings/branding"

var goodBrand = siteconfig.AdminBrand{
	Name: "Studio Console", LogoURL: "/media/2026/logo.png", Accent: "#1f4e9a",
	ManagedText: "Managed by Example Studio", ManagedURL: "https://example.test/studio",
}

func brandForm(b siteconfig.AdminBrand) url.Values {
	return url.Values{"name": {b.Name}, "logo": {b.LogoURL}, "accent": {b.Accent}, "managed_text": {b.ManagedText}, "managed_url": {b.ManagedURL}}
}

func TestBrandedLoginAndShell(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	g.a.deps.Site.(*fakeSite).cfg.AdminBrand = goodBrand

	rec := g.do("GET", "/admin/login", "", nil, nil)
	body := rec.Body.String()
	for _, want := range []string{
		`<img class="hm-brand__logo" src="/media/2026/logo.png" alt="Studio Console logo"`,
		"<span>Studio Console</span>",
		`<a href="https://example.test/studio" rel="noopener noreferrer" target="_blank">Managed by Example Studio</a>`,
		`hm-btn--accent`, "--color-accent:#1f4e9a",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("login lacks %q", want)
		}
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	compareGolden(t, "pages/auth-login-branded.html", normalizePage(body))

	shell := g.do("GET", "/admin/profile", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`<img class="hm-brand__logo" src="/media/2026/logo.png" alt=""`, "<span>Studio Console</span>", `class="hm-colophon__managed"`, `rel="noopener noreferrer"`} {
		if !strings.Contains(shell, want) {
			t.Errorf("shell lacks %q", want)
		}
	}
}

func TestBrandedLoginCSP(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	g.a.deps.Site.(*fakeSite).cfg.AdminBrand = goodBrand
	rec := g.do("GET", "/admin/login", "", nil, nil)
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("csp = %q", csp)
	}
	m := regexp.MustCompile(`'nonce-([^']+)'`).FindStringSubmatch(csp)
	if m == nil {
		t.Fatalf("csp has no nonce: %q", csp)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<style nonce="`+m[1]+`">html:root[data-admin-theme]{`) {
		t.Error("accent style lacks the request nonce")
	}
	if strings.Contains(body, ` style="`) {
		t.Error("the page emits a style attribute")
	}
}

func TestUnbrandedPagesCarryNoBranding(t *testing.T) {
	t.Parallel()

	body := newRig(t).do("GET", "/admin/login", "", nil, nil).Body.String()
	for _, bad := range []string{"<style", "hm-brand__logo", "hm-bare__managed", "hm-btn--accent"} {
		if strings.Contains(body, bad) {
			t.Errorf("unbranded login contains %q", bad)
		}
	}
}

func TestBrandingSave(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		mutate func(url.Values)
		code   int
	}{
		{"bright yellow", func(f url.Values) { f.Set("accent", "#f5e642") }, 422},
		{"mid grey", func(f url.Values) { f.Set("accent", "#999999") }, 422},
		{"not a hex", func(f url.Values) { f.Set("accent", "blue") }, 422},
		{"absolute logo", func(f url.Values) { f.Set("logo", "https://x.example/y.png") }, 422},
		{"svg logo", func(f url.Values) { f.Set("logo", "/media/a.svg") }, 422},
		{"javascript logo", func(f url.Values) { f.Set("logo", "javascript:alert(1)") }, 422},
		{"data logo", func(f url.Values) { f.Set("logo", "data:image/png;base64,AAAA") }, 422},
		{"javascript link", func(f url.Values) { f.Set("managed_url", "javascript:alert(1)") }, 422},
		{"ftp link", func(f url.Values) { f.Set("managed_url", "ftp://example.test/x") }, 422},
		{"link without text", func(f url.Values) { f.Set("managed_text", "") }, 422},
		{"long name", func(f url.Values) { f.Set("name", strings.Repeat("a", 61)) }, 422},
		{"good blue", func(f url.Values) {}, 303},
		{"upper case hex", func(f url.Values) { f.Set("accent", "#1F4E9A") }, 303},
		{"empty everything", func(f url.Values) {
			for k := range f {
				f.Set(k, "")
			}
		}, 303},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := newPerfRig(t, nil)
			site := p.a.deps.Site.(*fakeSite)
			form := brandForm(goodBrand)
			tc.mutate(form)
			rec := p.do("POST", brandingURL, "owner-token", form, nil)
			if rec.Code != tc.code {
				t.Fatalf("POST = %d, want %d", rec.Code, tc.code)
			}
			if tc.code == 422 {
				if site.brandWrites != 0 || len(p.events) != 0 {
					t.Errorf("a refused save stored %d writes, %d audit events", site.brandWrites, len(p.events))
				}
				return
			}
			if site.brandWrites != 1 || len(p.events) != 1 || p.events[0].Action != "branding.update" {
				t.Fatalf("writes=%d audit=%+v", site.brandWrites, p.events)
			}
			if loc := rec.Header().Get("Location"); loc != brandingURL {
				t.Errorf("Location = %q", loc)
			}
		})
	}
}

func TestBrandingContrastMessage(t *testing.T) {
	t.Parallel()

	p := newPerfRig(t, nil)
	f := brandForm(goodBrand)
	f.Set("accent", "#f5e642")
	body := p.do("POST", brandingURL, "owner-token", f, nil).Body.String()
	if !strings.Contains(body, "That colour is too light to read") || !strings.Contains(body, "4.5:1") {
		t.Errorf("no contrast message in %q", body)
	}
	if !strings.Contains(body, `value="#f5e642"`) {
		t.Error("the refused colour is not shown again")
	}
}

func TestBrandingReset(t *testing.T) {
	t.Parallel()

	p := newPerfRig(t, nil)
	site := p.a.deps.Site.(*fakeSite)
	site.cfg.AdminBrand = goodBrand
	page := p.do("GET", brandingURL, "owner-token", nil, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Reset to default") {
		t.Fatalf("page = %d", page.Code)
	}
	if issues := a11y.Check(page.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if rec := p.do("POST", brandingURL+"/reset", "owner-token", url.Values{}, nil); rec.Code != 303 {
		t.Fatalf("reset = %d", rec.Code)
	}
	if site.cfg.AdminBrand != (siteconfig.AdminBrand{}) {
		t.Errorf("brand after reset = %+v", site.cfg.AdminBrand)
	}
	if len(p.events) != 1 || p.events[0].Action != "branding.reset" {
		t.Errorf("audit = %+v", p.events)
	}
}

func TestBrandingOwnerOnly(t *testing.T) {
	t.Parallel()

	p := newPerfRig(t, nil)
	for _, path := range []string{brandingURL, brandingURL + "/reset"} {
		for _, method := range []string{"GET", "POST"} {
			if method == "GET" && path != brandingURL {
				continue
			}
			var form url.Values
			if method == "POST" {
				form = brandForm(goodBrand)
			}
			if rec := p.do(method, path, "editor-token", form, nil); rec.Code != 403 {
				t.Errorf("editor %s %s = %d", method, path, rec.Code)
			}
		}
	}
	if site := p.a.deps.Site.(*fakeSite); site.brandWrites != 0 {
		t.Error("an editor stored branding")
	}
}

// Every accepted accent keeps every declared pair passing with the override
// applied, in both themes, through the stylesheet the admin actually emits.
func TestAcceptedAccentsKeepEveryPair(t *testing.T) {
	t.Parallel()

	css := readAdminTokens(t)
	for _, hex := range []string{"#1f4e9a", "#000000", "#7a2a1c", "#2f6b3a", "#5b3a8c"} {
		if _, issues := a11y.CheckAccent(css, hex); len(issues) > 0 {
			t.Errorf("%s: %v", hex, issues)
		}
		if got := accentCSS(css, hex); got == "" {
			t.Errorf("%s: no stylesheet", hex)
		}
	}
	if got := accentCSS(css, "#f5e642"); got != "" {
		t.Errorf("a failing accent produced css %q", got)
	}
}

func readAdminTokens(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "admin", "css", "tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
