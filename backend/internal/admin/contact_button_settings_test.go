package admin

import (
	"encoding/json/jsontext"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/siteconfig"
)

func newContactButtonRig(t *testing.T) (*seoRig, *fakeSite) {
	t.Helper()
	s := newSEORig(t, newFakeSEO())
	site := newFakeSite("Acme Site")
	site.cfg.Phone = "+41 79 123 45 67"
	site.cfg.ThemeExtra = jsontext.Value(`{"builder":{"tokens":{}},"socialLinks":{"github":"https://g.example"}}`)
	s.a.deps.Site = themeSite{site}
	return s, site
}

func TestContactButtonPageGolden(t *testing.T) {
	s, _ := newContactButtonRig(t)
	body := s.get(t, "/admin/theme/contact-button")
	for _, want := range []string{"Show the floating button", "from your business details: &#43;41 79 123 45 67."} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	compareGolden(t, "pages/settings-contact-button.html", normalizePage(body))
}

func TestContactButtonWarnsWithoutNumber(t *testing.T) {
	s, site := newContactButtonRig(t)
	site.cfg.Phone = ""
	site.cfg.ThemeExtra = jsontext.Value(`{"contactButton":{"enabled":true,"channel":"whatsapp","position":"bottom-right"}}`)
	if body := s.get(t, "/admin/theme/contact-button"); !strings.Contains(body, "No phone number is set") {
		t.Error("no warning for an enabled button without a number")
	}
}

func TestContactButtonWarnsWhatsAppNumberWithoutCountryCode(t *testing.T) {
	s, site := newContactButtonRig(t)
	site.cfg.Phone = "044 668 18 00"
	site.cfg.ThemeExtra = jsontext.Value(`{"contactButton":{"enabled":true,"channel":"whatsapp","position":"bottom-right"}}`)
	if body := s.get(t, "/admin/theme/contact-button"); !strings.Contains(body, "has no country code") {
		t.Error("no warning for a WhatsApp button on a national-format number")
	}
}

func TestContactButtonSaveAuditsAndKeepsOtherKeys(t *testing.T) {
	s, site := newContactButtonRig(t)
	form := url.Values{
		"enabled": {"true"}, "channel": {"call"}, "position": {"bottom-left"},
		"label": {" Ring us "}, "to": {"+41 79 000 00 00"}, "message": {""},
	}
	rec := s.do("POST", "/admin/theme/contact-button", "owner-token", form, nil)
	if rec.Code != 303 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body.String())
	}
	got := siteconfig.ParseContactButton(site.cfg.ThemeExtra)
	want := siteconfig.ContactButton{Enabled: true, Channel: "call", Position: "bottom-left", Label: "Ring us", To: "+41 79 000 00 00"}
	if got != want {
		t.Errorf("saved = %+v", got)
	}
	if !strings.Contains(string(site.cfg.ThemeExtra), `"socialLinks"`) || !strings.Contains(string(site.cfg.ThemeExtra), `"builder"`) {
		t.Errorf("other keys lost: %s", site.cfg.ThemeExtra)
	}
	if s.flushed != 1 {
		t.Errorf("page cache cleared %d times, want 1", s.flushed)
	}
	if len(s.events) != 1 || s.events[0].Action != "contact_button.settings" {
		t.Fatalf("audit = %+v", s.events)
	}
	if strings.Contains(strings.ToLower(toJSON(t, s.events[0])), "000 00") {
		t.Error("the number is in the audit row")
	}
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := jsonx.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestContactButtonSaveRejectsInvalid(t *testing.T) {
	s, site := newContactButtonRig(t)
	before := string(site.cfg.ThemeExtra)
	rec := s.do("POST", "/admin/theme/contact-button", "owner-token", url.Values{
		"channel": {"fax"}, "position": {"top"}, "to": {"12"}, "label": {strings.Repeat("a", 61)},
	}, nil)
	if rec.Code != 422 {
		t.Fatalf("status %d, want 422", rec.Code)
	}
	if string(site.cfg.ThemeExtra) != before || s.flushed != 0 || len(s.events) != 0 {
		t.Error("an invalid post saved, audited or cleared the cache")
	}
	body := rec.Body.String()
	for _, want := range []string{"Choose WhatsApp, call", "Choose bottom right", "at most 60"} {
		if !strings.Contains(body, want) {
			t.Errorf("error %q not shown", want)
		}
	}
}

func TestContactButtonOwnerOnlyAndUnavailable(t *testing.T) {
	s, _ := newContactButtonRig(t)
	if rec := s.do("GET", "/admin/theme/contact-button", "editor-token", nil, nil); rec.Code == 200 {
		t.Error("an editor can open the page")
	}
	s.a.deps.Site = nil
	if rec := s.do("GET", "/admin/theme/contact-button", "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("no site store: status %d, want 503", rec.Code)
	}
}
