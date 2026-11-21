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

func TestParseUnixSocket(t *testing.T) {
	for in, want := range map[string]bool{"off": true, "OFF": true, "false": true, "0": true, "no": true, " off ": true,
		"": false, "on": false, "true": false, "1": false, "yes": false} {
		off, invalid := parseUnixSocket(in)
		if off != want || invalid != "" {
			t.Errorf("parseUnixSocket(%q) = %v, %q; want %v", in, off, invalid, want)
		}
	}
	if _, invalid := parseUnixSocket("maybe"); invalid != "maybe" {
		t.Fatalf("maybe: invalid = %q", invalid)
	}
}

func TestSplitSocketDirs(t *testing.T) {
	for in, want := range map[string]string{"/a:/b": "/a|/b", "/a, /b": "/a|/b", "": "", " : ,": ""} {
		got := splitSocketDirs(in)
		if strings.Join(got, "|") != want || (want == "" && got != nil) {
			t.Errorf("splitSocketDirs(%q) = %#v; want %q", in, got, want)
		}
	}
}

func TestDatabaseSocketValidate(t *testing.T) {
	if err := (DatabaseConfig{URL: "postgres://db/x"}).Validate(); err != nil {
		t.Fatalf("zero value: %v", err)
	}
	if err := (DatabaseConfig{SocketDirs: []string{"rel/dir"}}).Validate(); err == nil || !strings.Contains(err.Error(), "DATABASE_SOCKET_DIRS") {
		t.Fatalf("relative dir: %v", err)
	}
	if err := (DatabaseConfig{SocketDirs: []string{"/run/pg"}}).Validate(); err != nil {
		t.Fatalf("absolute dir: %v", err)
	}
	t.Setenv("DATABASE_URL", "postgres://db/x")
	t.Setenv("DATABASE_UNIX_SOCKET", "maybe")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Database.Validate(); err == nil || !strings.Contains(err.Error(), "DATABASE_UNIX_SOCKET") {
		t.Fatalf("invalid value: %v", err)
	}
}

func TestDatabaseSocketFromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://db/x")
	t.Setenv("DATABASE_UNIX_SOCKET", "off")
	t.Setenv("DATABASE_SOCKET_DIRS", "/a:/b")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Database.UnixSocketOff || strings.Join(cfg.Database.SocketDirs, "|") != "/a|/b" {
		t.Fatalf("got %+v", cfg.Database)
	}
}
