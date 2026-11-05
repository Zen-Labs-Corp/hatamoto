package server

import (
	"encoding/json"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/siteconfig"
)

func TestLocaleContact(t *testing.T) {
	set := businessSet(t, false, entity.LocaleEntry{Tag: "en"}, entity.LocaleEntry{Tag: "fr", Segment: "fr"}, entity.LocaleEntry{Tag: "it", Segment: "it"})
	cfg := &siteconfig.Config{
		BusinessProfile: json.RawMessage(`{"email":"site@example.test"}`),
		LocaleProfiles:  json.RawMessage(`{"fr":{"phone":"+33 1 23 45 67 89","email":"fr@example.test"}}`),
	}
	cases := []struct {
		name string
		loc  string
		want render.ContactView
	}{
		{"default has no phone", "en", render.ContactView{Email: "site@example.test"}},
		{"fr own phone and email", "fr", render.ContactView{Phone: "+33 1 23 45 67 89", Email: "fr@example.test"}},
		{"it inherits the site", "it", render.ContactView{Email: "site@example.test"}},
		{"unknown locale inherits", "", render.ContactView{Email: "site@example.test"}},
	}
	for _, c := range cases {
		if got := localeContact(cfg, set, c.loc); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
	// A one-locale site reads the site profile with the legacy phone merged in.
	one := businessSet(t, false, entity.LocaleEntry{Tag: "en"})
	cfg.Phone = "0791234567"
	if got := localeContact(cfg, one, "en"); got.Phone != "0791234567" || got.Email != "site@example.test" {
		t.Errorf("one locale: %+v", got)
	}
	// An unreadable profile is an empty contact, not an error.
	bad := &siteconfig.Config{BusinessProfile: json.RawMessage(`{`)}
	if got := localeContact(bad, one, "en"); got != (render.ContactView{}) {
		t.Errorf("bad profile: %+v", got)
	}
}

func TestContactFloatSettings(t *testing.T) {
	on := `{"contactButton":{"enabled":true,"channel":"call","position":"bottom-left","label":"Ring","to":"0791234567"}}`
	got := contactFloat(&siteconfig.Config{ThemeExtra: json.RawMessage(on)})
	want := render.ContactFloat{Enabled: true, Channel: "call", Position: "bottom-left", Label: "Ring", To: "0791234567"}
	if got != want {
		t.Errorf("enabled: %+v", got)
	}
	for _, in := range []string{``, `{}`, `{"contactButton":{"enabled":false,"channel":"call","position":"bottom-left"}}`,
		`{"contactButton":{"enabled":true,"channel":"fax","position":"bottom-left"}}`} {
		if g := contactFloat(&siteconfig.Config{ThemeExtra: json.RawMessage(in)}); g.Enabled {
			t.Errorf("%q must be disabled: %+v", in, g)
		}
	}
}
