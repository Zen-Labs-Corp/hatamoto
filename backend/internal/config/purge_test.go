package config

import (
	"strings"
	"testing"
)

func setPurgeEnv(t *testing.T, url, token, private string) {
	t.Helper()
	t.Setenv("HATAMOTO_PURGE_URL", url)
	t.Setenv("HATAMOTO_PURGE_TOKEN", token)
	t.Setenv("HATAMOTO_PURGE_ALLOW_PRIVATE", private)
}

func TestPurgeConfigUnsetIsOff(t *testing.T) {
	setPurgeEnv(t, "", "", "")
	p := loadPurgeConfig()
	if p != (PurgeConfig{}) || p.Validate() != nil {
		t.Errorf("got %+v, err %v", p, p.Validate())
	}
}

func TestPurgeConfigOff(t *testing.T) {
	for _, v := range []string{"off", "OFF", " Off "} {
		setPurgeEnv(t, v, "", "")
		p := loadPurgeConfig()
		if p.URL != "" || p.Validate() != nil {
			t.Errorf("%q: got %+v, err %v", v, p, p.Validate())
		}
	}
}

func TestPurgeConfigValidURL(t *testing.T) {
	setPurgeEnv(t, " https://cdn.example.com/purge ", "t0k", "true")
	p := loadPurgeConfig()
	if p.URL != "https://cdn.example.com/purge" || p.Token != "t0k" || !p.AllowPrivate || p.Validate() != nil {
		t.Errorf("got %+v, err %v", p, p.Validate())
	}
}

func TestPurgeConfigBadURL(t *testing.T) {
	for _, v := range []string{"ftp://x", "/relative", "https://user:pw@example.com/p", "https://example.com/p#frag", "https:///nohost"} {
		setPurgeEnv(t, v, "t0k", "")
		err := loadPurgeConfig().Validate()
		if err == nil || !strings.Contains(err.Error(), "HATAMOTO_PURGE_URL") || strings.Contains(err.Error(), "t0k") ||
			strings.Contains(err.Error(), "pw") || strings.Contains(err.Error(), "example.com") {
			t.Errorf("%q: err = %v", v, err)
		}
	}
}

func TestPurgeConfigOffIgnoresToken(t *testing.T) {
	setPurgeEnv(t, "off", "t0k", "")
	p := loadPurgeConfig()
	if err := p.Validate(); err != nil || p.URL != "" || p.Token != "" {
		t.Errorf("got %+v, err %v", p, err)
	}
}

func TestPurgeConfigTokenWithoutURL(t *testing.T) {
	setPurgeEnv(t, "", "t0k", "")
	err := loadPurgeConfig().Validate()
	if err == nil || !strings.Contains(err.Error(), "HATAMOTO_PURGE_TOKEN") || strings.Contains(err.Error(), "t0k") {
		t.Errorf("err = %v", err)
	}
}

func TestPurgeConfigAllowPrivate(t *testing.T) {
	setPurgeEnv(t, "https://cdn.example.com/p", "", "maybe")
	err := loadPurgeConfig().Validate()
	if err == nil || !strings.Contains(err.Error(), "HATAMOTO_PURGE_ALLOW_PRIVATE") {
		t.Errorf("err = %v", err)
	}
}

func TestPurgeConfigThroughLoad(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("HATAMOTO_SECRET", strings.Repeat("s", 32))
	setPurgeEnv(t, "https://cdn.example.com/purge", "t0k", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Purge.URL != "https://cdn.example.com/purge" {
		t.Errorf("Purge = %+v", cfg.Purge)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	setPurgeEnv(t, "ftp://x", "", "")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "HATAMOTO_PURGE_URL") {
		t.Errorf("Validate err = %v", err)
	}
}
