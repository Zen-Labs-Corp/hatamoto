package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePitchFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pitch.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDemoPitches(t *testing.T) {
	p := writePitchFile(t, `{
	  "home": {"title": "One binary", "body": "  The whole CMS is one program.  "},
	  "analytics": {"title": "AI analytics", "body": "Built in."},
	  "analytics/crawlers": {"body": "Checked against published ranges."}
	}`)
	m, err := LoadDemoPitches(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 3 || m["home"].Body != "The whole CMS is one program." || m["analytics/crawlers"].Title != "" {
		t.Fatalf("got %+v", m)
	}
}

func TestLoadDemoPitchesRejects(t *testing.T) {
	long := strings.Repeat("x", demoPitchBodyMax+1)
	for name, body := range map[string]string{
		"bad key":       `{"Analytics": {"body": "x"}}`,
		"deep key":      `{"a/b/c": {"body": "x"}}`,
		"path key":      `{"../etc": {"body": "x"}}`,
		"unknown field": `{"home": {"body": "x", "html": "<b>"}}`,
		"empty body":    `{"home": {"title": "t", "body": "  "}}`,
		"long title":    `{"home": {"title": "` + strings.Repeat("t", demoPitchTitleMax+1) + `", "body": "x"}}`,
		"long body":     `{"home": {"body": "` + long + `"}}`,
		"not an object": `["home"]`,
		"trailing data": `{"home": {"body": "x"}} {}`,
		"duplicate key": `{"home": {"body": "x"}, "home": {"body": "y"}}`,
		"too big":       `{"home": {"body": "x"}}` + strings.Repeat(" ", demoPitchFileMax),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadDemoPitches(writePitchFile(t, body)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	if _, err := LoadDemoPitches(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("a missing file was accepted")
	}
}

func TestDemoPitchFor(t *testing.T) {
	d := DemoConfig{Enabled: true, Pitches: map[string]DemoPitch{
		"home":               {Body: "home"},
		"analytics":          {Body: "section"},
		"analytics/crawlers": {Body: "page"},
	}}
	for rel, want := range map[string]string{
		"":                           "home",
		"/":                          "home",
		"/analytics":                 "section",
		"/analytics/traffic":         "section",
		"/analytics/crawlers":        "page",
		"/analytics/crawlers/gptbot": "page",
		"/plugins":                   "",
		"/settings/analytics":        "",
	} {
		p, ok := d.DemoPitchFor(rel)
		if got := map[bool]string{true: p.Body}[ok]; got != want {
			t.Errorf("%q: got %q, want %q", rel, got, want)
		}
	}
	if p, _ := d.DemoPitchFor("/analytics/crawlers"); p.Key != "analytics/crawlers" {
		t.Errorf("key %q", p.Key)
	}
	d.Enabled = false
	if _, ok := d.DemoPitchFor("/analytics"); ok {
		t.Error("a pitch outside demo mode")
	}
}

func TestLoadDemoConfigPitchFile(t *testing.T) {
	t.Setenv("HATAMOTO_DEMO_PITCH_FILE", writePitchFile(t, `{"home": {"body": "x"}}`))
	t.Setenv("HATAMOTO_DEMO", "1")
	d, err := LoadDemoConfig()
	if err != nil || d.Pitches["home"].Body != "x" {
		t.Fatalf("demo on: %+v, %v", d.Pitches, err)
	}

	// Off: the file is not read at all, so even a missing one is fine.
	t.Setenv("HATAMOTO_DEMO", "")
	t.Setenv("HATAMOTO_DEMO_PITCH_FILE", filepath.Join(t.TempDir(), "missing.json"))
	if d, err := LoadDemoConfig(); err != nil || d.Pitches != nil {
		t.Fatalf("demo off: %+v, %v", d.Pitches, err)
	}

	// On with a broken file: the server must not start.
	t.Setenv("HATAMOTO_DEMO", "1")
	if _, err := LoadDemoConfig(); err == nil {
		t.Fatal("a missing pitch file was accepted in demo mode")
	}
}
