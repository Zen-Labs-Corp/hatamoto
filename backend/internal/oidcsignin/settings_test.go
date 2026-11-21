package oidcsignin

import (
	"reflect"
	"strings"
	"testing"
)

func validSettings(t *testing.T) Settings {
	t.Helper()
	sealed, err := SealSecret(SecretKey("server-secret"), "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	return Settings{
		Enabled: true, Issuer: "https://idp.example.com/", ClientID: "cid",
		SecretSealed: sealed, AllowedDomains: []string{"acme.com"}, DefaultRole: "editor",
	}
}

func TestSettingsValidate(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*Settings)
		bad  bool
	}{
		{"valid", func(*Settings) {}, false},
		{"disabled and empty", func(s *Settings) { *s = Settings{DefaultRole: "editor"} }, false},
		{"owner role", func(s *Settings) { s.DefaultRole = "owner" }, false},
		{"bad role", func(s *Settings) { s.DefaultRole = "root" }, true},
		{"http issuer", func(s *Settings) { s.Issuer = "http://idp.example.com" }, true},
		{"issuer query", func(s *Settings) { s.Issuer = "https://idp.example.com/?a=b" }, true},
		{"issuer fragment", func(s *Settings) { s.Issuer = "https://idp.example.com/#x" }, true},
		{"issuer userinfo", func(s *Settings) { s.Issuer = "https://u:p@idp.example.com" }, true},
		{"issuer no host", func(s *Settings) { s.Issuer = "https:///x" }, true},
		{"issuer too long", func(s *Settings) { s.Issuer = "https://a.example.com/" + strings.Repeat("a", MaxIssuer) }, true},
		{"label too long", func(s *Settings) { s.Label = strings.Repeat("é", MaxLabel+1) }, true},
		{"label control", func(s *Settings) { s.Label = "a\nb" }, true},
		{"client id control", func(s *Settings) { s.ClientID = "a\x00b" }, true},
		{"client id too long", func(s *Settings) { s.ClientID = strings.Repeat("a", MaxClientID+1) }, true},
		{"enabled no issuer", func(s *Settings) { s.Issuer = "" }, true},
		{"enabled no client id", func(s *Settings) { s.ClientID = "" }, true},
		{"enabled no secret", func(s *Settings) { s.SecretSealed = nil }, true},
		{"enabled no domain", func(s *Settings) { s.AllowedDomains = nil }, true},
		{"bad domain", func(s *Settings) { s.AllowedDomains = []string{"not a domain"} }, true},
		{"disabled incomplete", func(s *Settings) { s.Enabled = false; s.SecretSealed = nil; s.AllowedDomains = nil }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validSettings(t)
			tt.mut(&s)
			if err := s.Validate(); (err != nil) != tt.bad {
				t.Fatalf("Validate() = %v, bad = %v", err, tt.bad)
			}
		})
	}
}

func TestParseDomains(t *testing.T) {
	got, err := ParseDomains(" @Acme.com, example.org\nacme.com  sub.example.org ")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"acme.com", "example.org", "sub.example.org"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, bad := range []string{"localhost", "a@b.com", "-x.com", "a..com", "exa mple.com"} { // the last splits into two fields
		if _, err := ParseDomains(bad); err == nil {
			t.Errorf("ParseDomains(%q) accepted", bad)
		}
	}
	var many []string
	for i := 0; i <= MaxDomains; i++ {
		many = append(many, "d"+strings.Repeat("a", i)+".com")
	}
	if _, err := ParseDomains(strings.Join(many, ",")); err == nil {
		t.Error("too many domains accepted")
	}
	if got, err := ParseDomains(""); err != nil || len(got) != 0 {
		t.Errorf("empty = %v, %v", got, err)
	}
}

func TestSecretRoundTrip(t *testing.T) {
	key := SecretKey("server-secret")
	sealed, err := SealSecret(key, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	s := Settings{SecretSealed: sealed}
	if got, ok := s.Secret(key); !ok || got != "s3cret" {
		t.Fatalf("Secret = %q, %v", got, ok)
	}
	if _, ok := s.Secret(SecretKey("other")); ok {
		t.Fatal("opened with the wrong key")
	}
	if _, ok := (Settings{}).Secret(key); ok || (Settings{}).HasSecret() {
		t.Fatal("empty settings report a secret")
	}
	if !s.HasSecret() {
		t.Fatal("HasSecret = false")
	}
}

func TestDisplayLabel(t *testing.T) {
	if got := (Settings{}).DisplayLabel(); got != DefaultLabel {
		t.Fatal(got)
	}
	if got := (Settings{Label: " Acme SSO "}).DisplayLabel(); got != "Acme SSO" {
		t.Fatal(got)
	}
}
