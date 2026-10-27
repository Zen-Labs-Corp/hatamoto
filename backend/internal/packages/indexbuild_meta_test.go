package packages

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// metaDir writes one hello plugin as hello.hmp and one theme as sunrise.hmp.
func metaDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, files := range map[string]map[string][]byte{"hello.hmp": pluginFiles(), "sunrise.hmp": themeFiles()} {
		if err := os.WriteFile(filepath.Join(dir, name), packForMeta(t, files), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func buildMeta(t *testing.T, dir string, meta map[string]Meta) ([]byte, error) {
	t.Helper()
	key := newKey(t)
	doc, err := BuildIndexWith(dir, "p/", key, time.Now(), time.Hour, BuildOptions{Meta: meta})
	if err != nil {
		return nil, err
	}
	return trust(t, key).VerifyIndex(doc)
}

func goodMeta() Meta {
	return Meta{Title: "Hello Plugin", Author: "acme", Source: "https://example.org/acme/hello", Commit: strings.Repeat("a", 40), Tier: TierCommunity}
}

func TestBuildIndexWithoutMetaOmitsTheFields(t *testing.T) {
	raw, err := buildMeta(t, metaDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"author"`, `"source"`, `"commit"`, `"tier"`, `"screenshot"`} {
		if strings.Contains(string(raw), k) {
			t.Errorf("index without meta contains %s: %s", k, raw)
		}
	}
}

func TestBuildIndexWithMetaWritesFields(t *testing.T) {
	raw, err := buildMeta(t, metaDir(t), map[string]Meta{"hello": goodMeta()})
	if err != nil {
		t.Fatal(err)
	}
	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatal(err)
	}
	e, ok := idx.Find(KindPlugin, "hello")
	if !ok || e.Title != "Hello Plugin" || e.Author != "acme" || e.Tier != TierCommunity || e.Commit != strings.Repeat("a", 40) || e.Source != "https://example.org/acme/hello" {
		t.Fatalf("hello entry: %+v", e)
	}
	if th, _ := idx.Find(KindTheme, "sunrise"); th.Tier != "" || th.Author != "" {
		t.Errorf("a package without meta gained fields: %+v", th)
	}
}

func TestBuildIndexRejectsBadMeta(t *testing.T) {
	dir := metaDir(t)
	with := func(edit func(*Meta)) map[string]Meta {
		m := goodMeta()
		edit(&m)
		return map[string]Meta{"hello": m}
	}
	cases := map[string]map[string]Meta{
		"unknown name":     {"ghost": goodMeta()},
		"http source":      with(func(m *Meta) { m.Source = "http://example.org/x" }),
		"javascript":       with(func(m *Meta) { m.Source = "javascript:alert(1)" }),
		"credentials":      with(func(m *Meta) { m.Source = "https://u:p@example.org/x" }),
		"no host":          with(func(m *Meta) { m.Source = "https:///x" }),
		"short commit":     with(func(m *Meta) { m.Commit = "abc123" }),
		"uppercase commit": with(func(m *Meta) { m.Commit = strings.Repeat("A", 40) }),
		"missing tier":     with(func(m *Meta) { m.Tier = "" }),
		"unknown tier":     with(func(m *Meta) { m.Tier = "gold" }),
		"long title":       with(func(m *Meta) { m.Title = strings.Repeat("t", 61) }),
		"control title":    with(func(m *Meta) { m.Title = "a\nb" }),
		"padded title":     with(func(m *Meta) { m.Title = " Hello" }),
		"tab title":        with(func(m *Meta) { m.Title = "a\tb" }),
		"c1 title":         with(func(m *Meta) { m.Title = "a\u0085b" }),
		"bad author":       with(func(m *Meta) { m.Author = `"><img src=x>` }),
	}
	for name, meta := range cases {
		if _, err := buildMeta(t, dir, meta); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	_, err := buildMeta(t, dir, map[string]Meta{"ghost": goodMeta()})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("unknown name error = %v, want it to name ghost", err)
	}
}

func TestMetaTitleAccepts(t *testing.T) {
	dir := metaDir(t)
	for name, title := range map[string]string{
		"no-break space": "Dev\u00a0Tools",
		"zwj emoji":      "Code \U0001F468\u200d\U0001F4BB",
		"japanese":       strings.Repeat("\u65e5", 25),
		"60 runes":       strings.Repeat("\u65e5", 60),
	} {
		m := goodMeta()
		m.Title = title
		if _, err := buildMeta(t, dir, map[string]Meta{"hello": m}); err != nil {
			t.Errorf("%s: rejected: %v", name, err)
		}
	}
}

func TestBuildIndexRejectsDuplicateNames(t *testing.T) {
	dir := metaDir(t)
	if err := os.WriteFile(filepath.Join(dir, "other.hmp"), packForMeta(t, pluginFiles()), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := buildMeta(t, dir, nil); err == nil || !strings.Contains(err.Error(), "hello") {
		t.Fatalf("err = %v, want a duplicate-name refusal", err)
	}
}

func TestBuildIndexAllowsAThemeAndPluginWithOneName(t *testing.T) {
	dir := metaDir(t)
	th := themeFiles()
	th[ThemeManifest] = []byte(strings.Replace(themeJSON, "sunrise", "hello", 1))
	if err := os.WriteFile(filepath.Join(dir, "hello-theme.hmp"), packForMeta(t, th), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := buildMeta(t, dir, nil)
	if err != nil {
		t.Fatalf("theme and plugin named hello refused: %v", err)
	}
	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.Find(KindPlugin, "hello"); !ok {
		t.Error("plugin hello missing")
	}
	if _, ok := idx.Find(KindTheme, "hello"); !ok {
		t.Error("theme hello missing")
	}
	_, err = buildMeta(t, dir, map[string]Meta{"hello": goodMeta()})
	if err == nil || !strings.Contains(err.Error(), "both a plugin and a theme") {
		t.Fatalf("meta for a name used by both kinds: err = %v", err)
	}
}

func TestParseMeta(t *testing.T) {
	m, err := ParseMeta([]byte(`{"hello":{"author":"a","tier":"community"}}`))
	if err != nil || m["hello"].Author != "a" {
		t.Fatalf("%+v, %v", m, err)
	}
	for _, bad := range []string{`{"hello":{"autor":"a","tier":"community"}}`, `[]`, `{} {}`, `nope`} {
		if _, err := ParseMeta([]byte(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func packForMeta(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	b, err := PackFiles(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBuildIndexScreenshotMeta(t *testing.T) {
	dir := metaDir(t)
	theme := func(shot string) map[string]Meta {
		m := goodMeta()
		m.Screenshot = shot
		return map[string]Meta{"sunrise": m}
	}
	raw, err := buildMeta(t, dir, theme("screenshots/sunrise.webp"))
	if err != nil {
		t.Fatal(err)
	}
	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatal(err)
	}
	if e, _ := idx.Find(KindTheme, "sunrise"); e.Screenshot != "screenshots/sunrise.webp" {
		t.Errorf("screenshot = %q", e.Screenshot)
	}
	for name, shot := range map[string]string{
		"wrong name": "screenshots/other.png",
		"traversal":  "../x.png",
		"nested":     "screenshots/a/sunrise.png",
		"svg":        "screenshots/sunrise.svg",
	} {
		if _, err := buildMeta(t, dir, theme(shot)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	plugin := goodMeta()
	plugin.Screenshot = "screenshots/hello.png"
	if _, err := buildMeta(t, dir, map[string]Meta{"hello": plugin}); err == nil {
		t.Error("plugin with screenshot accepted")
	}
}
