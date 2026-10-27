package main

import (
	"errors"
	"strings"
	"testing"
)

func mapEnv(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func memFS(files map[string]string) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		if s, ok := files[p]; ok {
			return []byte(s), nil
		}
		return nil, errors.New("not found")
	}
}

func TestLoadCredsEnvBeatsFile(t *testing.T) {
	files := memFS(map[string]string{"a.env": "HATAMOTO_URL=https://file.example\nHATAMOTO_API_KEY=filekey\nHATAMOTO_CHANGELOG_SCHEMA_ID=sid\n"})
	c, err := loadCreds(mapEnv(map[string]string{envURL: "https://env.example"}), files, []string{"a.env"})
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "https://env.example" || c.Key != "filekey" || c.SchemaID != "sid" {
		t.Errorf("creds = %+v", c)
	}
}

func TestLoadCredsFirstExistingFileOnly(t *testing.T) {
	files := memFS(map[string]string{
		"second.env": "HATAMOTO_URL=u\nHATAMOTO_API_KEY=second\nHATAMOTO_CHANGELOG_SCHEMA_ID=s\n",
		"third.env":  "HATAMOTO_API_KEY=third\n",
	})
	c, err := loadCreds(mapEnv(nil), files, []string{"first.env", "second.env", "third.env"})
	if err != nil || c.Key != "second" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestParseEnvFile(t *testing.T) {
	m := parseEnvFile("# comment\n\nexport A=1\nB = \"two words\"\nC='q'\nnoequals\nD=x=y\n")
	want := map[string]string{"A": "1", "B": "two words", "C": "q", "D": "x=y"}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %q, want %q", k, m[k], v)
		}
	}
	if len(m) != len(want) {
		t.Errorf("m = %v", m)
	}
}

func TestLoadCredsMissingNamesOnly(t *testing.T) {
	_, err := loadCreds(mapEnv(map[string]string{envKey: "supersecret"}), memFS(nil), nil)
	if err == nil || strings.Contains(err.Error(), "supersecret") ||
		!strings.Contains(err.Error(), envURL) || !strings.Contains(err.Error(), envSchema) || strings.Contains(err.Error(), envKey) {
		t.Errorf("err = %v", err)
	}
}
