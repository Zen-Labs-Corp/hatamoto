package config

import (
	"strings"
	"testing"
)

func TestEffectiveDirectURL(t *testing.T) {
	d := DatabaseConfig{URL: "postgres://pooled/db"}
	if got := d.EffectiveDirectURL(); got != d.URL {
		t.Fatalf("unset: got %q, want the main URL", got)
	}
	d.DirectURL = "postgres://direct/db"
	if got := d.EffectiveDirectURL(); got != "postgres://direct/db" {
		t.Fatalf("set: got %q", got)
	}
}

func TestDatabaseDirectURLFromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://pooled/db")
	t.Setenv("DATABASE_DIRECT_URL", "  postgres://direct/db  ")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Database.EffectiveDirectURL(); got != "postgres://direct/db" {
		t.Fatalf("got %q", got)
	}
}

func TestDatabaseDirectURLValidate(t *testing.T) {
	ok := DatabaseConfig{DirectURL: "postgres://u:p@h:5432/db?pool_max_conns=3"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid URL rejected: %v", err)
	}
	bad := DatabaseConfig{DirectURL: "postgres://u:secret@h:notaport/db"}
	err := bad.Validate()
	if err == nil {
		t.Fatal("invalid URL accepted")
	}
	if s := err.Error(); strings.Contains(s, "secret") {
		t.Fatalf("error leaks the URL: %q", s)
	}
}
