package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadConfigEnv(t *testing.T) {
	t.Parallel()
	c, err := LoadConfig(env(map[string]string{"HATAMOTO_URL": "https://example.com/", "HATAMOTO_API_KEY": "k"}), "")
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "https://example.com" || c.APIKey != "k" {
		t.Fatalf("%+v", c)
	}
}

func TestLoadConfigFile(t *testing.T) {
	t.Parallel()
	f := filepath.Join(t.TempDir(), "x.env")
	body := "# c\nHATAMOTO_URL=http://127.0.0.1:8080\nexport HATAMOTO_API_KEY=\"sekret\"\n"
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(env(nil), f)
	if err != nil {
		t.Fatal(err)
	}
	if c.APIKey != "sekret" || c.URL != "http://127.0.0.1:8080" {
		t.Fatalf("%+v", c)
	}
	if s := fmt.Sprintf("%v %+v %#v", c, c, c); strings.Contains(s, "sekret") {
		t.Fatalf("key leaked in formatted output: %s", s)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	t.Parallel()
	cases := map[string]map[string]string{
		"missing key":  {"HATAMOTO_URL": "https://example.com"},
		"plain http":   {"HATAMOTO_URL": "http://example.com", "HATAMOTO_API_KEY": "k"},
		"bad url":      {"HATAMOTO_URL": "::", "HATAMOTO_API_KEY": "k"},
		"missing both": {},
	}
	for name, m := range cases {
		if _, err := LoadConfig(env(m), ""); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, err := LoadConfig(env(nil), filepath.Join(t.TempDir(), "absent.env")); err == nil {
		t.Error("explicit missing env file must fail")
	}
}

func TestRunLintNeedsNoCredentials(t *testing.T) {
	t.Parallel()
	var out, errb strings.Builder
	root := site(t, baseFiles())
	if code := run([]string{"--lint", "--root", root}, env(nil), &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if code := run([]string{"--root", root}, env(nil), &out, &errb); code != exitUsage {
		t.Fatalf("no credentials: code %d", code)
	}
}

func TestLoadConfigRelativeToBaseDir(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	body := "HATAMOTO_URL=https://example.com\nHATAMOTO_API_KEY=k\n"
	if err := os.WriteFile(filepath.Join(base, defaultEnvFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "other.env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	getenv := env(map[string]string{baseDirEnv: base})
	for _, file := range []string{"", "other.env"} {
		c, err := LoadConfig(getenv, file)
		if err != nil || c.APIKey != "k" {
			t.Fatalf("env file %q: %+v %v", file, c, err)
		}
	}
	if _, err := LoadConfig(getenv, "missing.env"); err == nil {
		t.Fatal("an explicit file that does not exist must fail")
	}
}
