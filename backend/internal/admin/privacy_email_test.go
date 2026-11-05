package admin

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/siteconfig"
)

func newEmailProtectRig(t *testing.T) (*seoRig, *fakeSite) {
	t.Helper()
	s, _ := newPrivacyRig(t)
	site := newFakeSite("Acme Site")
	site.cfg.ThemeExtra = json.RawMessage(`{"socialLinks":{"github":"https://g.example"}}`)
	s.a.deps.Site = themeSite{site}
	return s, site
}

func TestPrivacyShowsEmailProtectionOnByDefault(t *testing.T) {
	s, _ := newEmailProtectRig(t)
	body := s.get(t, "/admin/settings/privacy")
	i := strings.Index(body, `id="privacy-protect-email"`)
	if i < 0 || !strings.Contains(body, "Protect email addresses") {
		t.Fatal("page lacks the email protection switch")
	}
	tag := body[strings.LastIndex(body[:i], "<"):]
	tag = tag[:strings.Index(tag, ">")]
	if !strings.Contains(tag, "checked") {
		t.Errorf("switch not checked by default: %s", tag)
	}
}

func TestEmailProtectionSaveAuditsAndClearsCache(t *testing.T) {
	s, site := newEmailProtectRig(t)
	rec := s.do("POST", "/admin/settings/privacy/email-protection", "owner-token", url.Values{"protect_email": {"false"}}, nil)
	if rec.Code != 303 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body.String())
	}
	if siteconfig.ParseProtectEmail(site.cfg.ThemeExtra) {
		t.Errorf("stored = %s, want off", site.cfg.ThemeExtra)
	}
	if !strings.Contains(string(site.cfg.ThemeExtra), "socialLinks") {
		t.Error("other theme settings lost")
	}
	if s.flushed != 1 {
		t.Errorf("page cache cleared %d times, want 1", s.flushed)
	}
	if len(s.events) != 1 || s.events[0].Action != "email_protection.settings" {
		t.Errorf("audit = %+v", s.events)
	}
	s.do("POST", "/admin/settings/privacy/email-protection", "owner-token", url.Values{"protect_email": {"true"}}, nil)
	if !siteconfig.ParseProtectEmail(site.cfg.ThemeExtra) {
		t.Error("switch back on not stored")
	}
}

func TestEmailProtectionOwnerOnly(t *testing.T) {
	s, site := newEmailProtectRig(t)
	rec := s.do("POST", "/admin/settings/privacy/email-protection", "editor-token", url.Values{"protect_email": {"false"}}, nil)
	if rec.Code == 303 || !siteconfig.ParseProtectEmail(site.cfg.ThemeExtra) {
		t.Errorf("an editor changed the setting (status %d)", rec.Code)
	}
}
