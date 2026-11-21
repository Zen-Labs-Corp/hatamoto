package server

import (
	"encoding/json/jsontext"
	"reflect"
	"sync"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/siteconfig"
)

func siteDerivedConfig(primary string) *siteconfig.Config {
	return &siteconfig.Config{
		CompanyName: "Example",
		ThemeExtra: jsontext.Value(`{"colors":{"primary":"` + primary + `"},` +
			`"socialLinks":{"x":"https://example.com/x"},` +
			`"builder":{"tokens":{"color":{"accent":"#111111"}}},"custom":"v"}`),
		BusinessProfile: jsontext.Value(`{"name":"Example Legal","sameas":["https://example.com/a","https://example.com/b"]}`),
	}
}

func TestSiteDerivedDecodesOncePerConfig(t *testing.T) {
	s := &Server{}
	cfg := siteDerivedConfig("#123456")
	d1 := s.siteDerivedFor(cfg)
	for range 50 {
		if s.siteDerivedFor(cfg) != d1 {
			t.Fatal("memo recomputed for the same config pointer")
		}
	}
}

func TestSiteDerivedMatchesDirectDecode(t *testing.T) {
	cfg := siteDerivedConfig("#123456")
	d := (&Server{}).siteDerivedFor(cfg)
	org, orgErr := orgView(cfg)
	design, designErr := builder.ParseDesign(cfg.ThemeExtra)
	for _, c := range []struct {
		name      string
		got, want any
	}{
		{"theme", d.theme, effectiveTheme(cfg)},
		{"social", d.social, socialLinks(cfg.ThemeExtra)},
		{"contact", d.contact, contactFloat(cfg)},
		{"address", d.address, postalAddress(cfg.PostalAddress)},
		{"org", d.org, org},
		{"orgErr", d.orgErr, orgErr},
		{"orgAddress", d.orgAddress, orgAddressMode(cfg)},
		{"settings", d.settings, siteSettings(cfg.ThemeExtra)},
		{"cssName", d.cssName, render.CustomCSSName(render.AdditionalCSS(cfg.ThemeExtra))},
		{"emailPlain", d.emailPlain, !siteconfig.ParseProtectEmail(cfg.ThemeExtra)},
		{"design", d.design, design},
		{"designErr", d.designErr, designErr},
	} {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s: got %#v want %#v", c.name, c.got, c.want)
		}
	}
	if len(d.social) == 0 || len(d.org.SameAs) != 2 || d.design.Tokens.Color["accent"] != "#111111" {
		t.Fatalf("fixture did not decode: %#v", d)
	}
}

func TestSiteDerivedNewConfigPointer(t *testing.T) {
	s := &Server{}
	d1 := s.siteDerivedFor(siteDerivedConfig("#123456"))
	d2 := s.siteDerivedFor(siteDerivedConfig("#654321"))
	if d1 == d2 {
		t.Fatal("a new config pointer reused the memo")
	}
	if got := d2.theme.Colors.Primary; got != "#654321" {
		t.Fatalf("primary = %q, want #654321", got)
	}
}

func TestSiteDerivedCopiesAreIsolated(t *testing.T) {
	d := (&Server{}).siteDerivedFor(siteDerivedConfig("#123456"))
	var first render.SiteView
	d.apply(&first)
	first.Settings["custom"] = "changed"
	first.SocialLinks[0].URL = "changed"
	first.Design.Tokens.Color["accent"] = "changed"
	first.Org.SameAs[0] = "changed"

	var second render.SiteView
	d.apply(&second)
	if second.Settings["custom"] != "v" {
		t.Errorf("Settings leaked: %v", second.Settings["custom"])
	}
	if second.SocialLinks[0].URL != "https://example.com/x" {
		t.Errorf("SocialLinks leaked: %v", second.SocialLinks[0])
	}
	if second.Design.Tokens.Color["accent"] != "#111111" {
		t.Errorf("Design leaked: %v", second.Design.Tokens.Color)
	}
	if second.Org.SameAs[0] != "https://example.com/a" {
		t.Errorf("Org.SameAs leaked: %v", second.Org.SameAs)
	}

	cfg := siteDerivedConfig("#123456")
	cfg.ThemeExtra = jsontext.Value(`{"headerCta":{"text":"Go","url":"/go"}}`)
	dc := (&Server{}).siteDerivedFor(cfg)
	if dc.theme.HeaderCTA == nil {
		t.Fatal("theme did not decode headerCta from theme_extra: the isolation check cannot run")
	}
	t1 := dc.themeCopy()
	t1.HeaderCTA.Text = "changed"
	if dc.themeCopy().HeaderCTA.Text != "Go" {
		t.Error("HeaderCTA leaked")
	}
}

func TestSiteDerivedConcurrent(t *testing.T) {
	s := &Server{}
	cfg := siteDerivedConfig("#123456")
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 50 {
				d := s.siteDerivedFor(cfg)
				var site render.SiteView
				d.apply(&site)
				site.Settings["custom"] = "mine"
				site.SocialLinks[0].URL = "mine"
				site.Design.Tokens.Color["accent"] = "mine"
				site.Org.SameAs[0] = "mine"
				_ = d.themeCopy()
			}
		})
	}
	wg.Wait()
}
