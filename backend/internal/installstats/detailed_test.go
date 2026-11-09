package installstats

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/jsonx"
)

// validateDetailed mirrors the receiver's rules for a Detailed body (the
// contract in the install-diagnostics plan) so drift between sender and
// receiver is caught here.
func validateDetailed(body []byte) error {
	if len(body) > maxDetailedBytes {
		return errors.New("body over 16 KiB")
	}
	entries, err := jsonx.ObjectEntries(body)
	if err != nil {
		return err
	}
	want := []string{"install", "version", "channel", "level", "os", "arch", "postgres", "age_months",
		"worker", "theme", "plugins", "other_plugins", "health"}
	if len(entries) != len(want) {
		return fmt.Errorf("%d top-level keys, want %d", len(entries), len(want))
	}
	for i, e := range entries {
		if e.Key != want[i] {
			return fmt.Errorf("key %d is %q, want %q", i, e.Key, want[i])
		}
	}
	var b struct {
		Install      string           `json:"install"`
		Version      string           `json:"version"`
		Channel      string           `json:"channel"`
		Level        string           `json:"level"`
		OS           string           `json:"os"`
		Arch         string           `json:"arch"`
		Postgres     int              `json:"postgres"`
		AgeMonths    int              `json:"age_months"`
		OtherPlugins int              `json:"other_plugins"`
		Worker       bool             `json:"worker"`
		Theme        detailedTheme    `json:"theme"`
		Plugins      []detailedPlugin `json:"plugins"`
		Health       map[string]any   `json:"health"`
	}
	if err := jsonx.Unmarshal(body, &b, jsonx.StrictNoUnknown); err != nil {
		return err
	}
	switch {
	case b.Level != "detailed":
		return errors.New("level")
	case !osArchRE.MatchString(b.OS) || !osArchRE.MatchString(b.Arch):
		return errors.New("os/arch")
	case b.Postgres < 9 || b.Postgres > 99:
		return errors.New("postgres")
	case ageBucket(b.AgeMonths) != b.AgeMonths:
		return errors.New("age bucket")
	case !nameRE.MatchString(b.Theme.Name) || !validVersion(b.Theme.Version):
		return errors.New("theme")
	case len(b.Plugins) > maxPlugins || b.OtherPlugins < 0 || b.OtherPlugins > maxOtherPlugins:
		return errors.New("plugin counts")
	}
	listed := map[string]bool{}
	for i, p := range b.Plugins {
		if !nameRE.MatchString(p.Name) || !validVersion(p.Version) {
			return fmt.Errorf("plugin %q", p.Name)
		}
		if i > 0 && b.Plugins[i-1].Name >= p.Name {
			return errors.New("plugins not sorted")
		}
		listed[p.Name] = true
	}
	healthKeys := []string{"since_hours", "panics", "http_5xx", "render_errors", "plugin_errors",
		"update_failed", "update_rolled_back", "migration_failed"}
	if len(b.Health) != len(healthKeys) {
		return errors.New("health keys")
	}
	for _, k := range healthKeys {
		v, ok := b.Health[k]
		if !ok {
			return fmt.Errorf("health.%s missing", k)
		}
		if k == "plugin_errors" {
			m, _ := v.(map[string]any)
			if m == nil || len(m) > maxErrorKeys {
				return errors.New("plugin_errors")
			}
			for name, n := range m {
				if name != otherKey && !listed[name] {
					return fmt.Errorf("plugin_errors names unlisted %q", name)
				}
				if f, _ := n.(float64); f < 0 || f > maxCount {
					return errors.New("plugin_errors range")
				}
			}
			continue
		}
		f, _ := v.(float64)
		limit := float64(maxCount)
		if k == "since_hours" {
			limit = maxSinceHours
		}
		if f < 0 || f > limit {
			return fmt.Errorf("health.%s out of range", k)
		}
	}
	return nil
}

type fakeSources struct {
	plugins        []PluginInfo
	theme          ThemeInfo
	pg             int
	start          time.Time
	startErr       error
	failed, rolled int64
	updErr         error
	since          time.Time
}

func (f *fakeSources) Plugins(context.Context) ([]PluginInfo, error) { return f.plugins, nil }
func (f *fakeSources) Theme(context.Context) (ThemeInfo, error)      { return f.theme, nil }
func (f *fakeSources) PostgresMajor(context.Context) (int, error)    { return f.pg, nil }
func (f *fakeSources) InstallStart(context.Context) (time.Time, error) {
	return f.start, f.startErr
}
func (f *fakeSources) UpdateCounts(_ context.Context, since time.Time) (int64, int64, error) {
	f.since = since
	return f.failed, f.rolled, f.updErr
}
func (f *fakeSources) WorkerOn() bool { return true }

var testNow = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func newFake() *fakeSources {
	return &fakeSources{
		theme: ThemeInfo{Name: "hatamoto-dev", Version: "1.6.0"}, pg: 16,
		start: testNow.AddDate(0, -6, -3),
	}
}

func build(t *testing.T, f *fakeSources, c Counts, window *time.Time) string {
	t.Helper()
	b, err := BuildDetailed(context.Background(), BuildInput{
		Install: strings.Repeat("a", 32), Version: "v1.1.0", Channel: "stable",
		Src: f, Counts: c, WindowStart: window, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDetailed(b); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	return string(b)
}

func TestBuildDetailedExactBody(t *testing.T) {
	f := newFake()
	f.plugins = []PluginInfo{
		{Name: "searchconsole", Version: "1.0.0", Enabled: true, FirstParty: true},
		{Name: "my-private-thing", Version: "0.1.0", Enabled: true},
		{Name: "another-private", Version: "2.0.0"},
	}
	f.failed, f.rolled = 1, 2
	win := testNow.Add(-24 * time.Hour)
	got := build(t, f, Counts{HTTP5xx: 3, PluginErrors: map[string]int64{"searchconsole": 1, "my-private-thing": 4}}, &win)
	want := `{"install":"` + strings.Repeat("a", 32) + `","version":"v1.1.0","channel":"stable","level":"detailed",` +
		`"os":"` + runtime.GOOS + `","arch":"` + runtime.GOARCH + `","postgres":16,"age_months":6,"worker":true,` +
		`"theme":{"name":"hatamoto-dev","version":"1.6.0"},` +
		`"plugins":[{"name":"searchconsole","version":"1.0.0","enabled":true}],"other_plugins":2,` +
		`"health":{"since_hours":24,"panics":0,"http_5xx":3,"render_errors":0,` +
		`"plugin_errors":{"other":4,"searchconsole":1},"update_failed":1,"update_rolled_back":2,"migration_failed":0}}`
	// Map key order is not part of the contract; compare after normalising it.
	if strings.Replace(got, `"searchconsole":1,"other":4`, `"other":4,"searchconsole":1`, 1) != want {
		t.Fatalf("body\n got %s\nwant %s", got, want)
	}
	if strings.Contains(got, "private") {
		t.Fatal("self-built plugin name leaked into the body")
	}
	if !f.since.Equal(win) {
		t.Fatalf("update counts asked since %v, want window start %v", f.since, win)
	}
}

func TestBuildDetailedNilWindow(t *testing.T) {
	f := newFake()
	got := build(t, f, Counts{}, nil)
	if !strings.Contains(got, `"since_hours":0`) {
		t.Fatalf("nil window should report 0 hours: %s", got)
	}
	if !f.since.Equal(testNow.Add(-24 * time.Hour)) {
		t.Fatalf("update counts since %v, want now-24h", f.since)
	}
}

func TestBuildDetailedPluginRules(t *testing.T) {
	fp := func(name, version string) PluginInfo {
		return PluginInfo{Name: name, Version: version, Enabled: true, FirstParty: true}
	}
	cases := []struct {
		name      string
		plugins   []PluginInfo
		listed    string // substring that must be in the body
		unlisted  string // substring that must not be
		wantOther string
	}{
		{"invalid name dropped", []PluginInfo{fp("Bad_Name", "1.0.0"), fp("good", "1.0.0")}, `"name":"good"`, "Bad_Name", `"other_plugins":1`},
		{"invalid version dropped", []PluginInfo{fp("a", "v1"), fp("b", "1.0.0")}, `"name":"b"`, `"name":"a"`, `"other_plugins":1`},
		{"leading v dropped", []PluginInfo{fp("a", "v1.0.0")}, "", `"name":"a"`, `"other_plugins":1`},
		{"build metadata stripped", []PluginInfo{fp("a", "1.0.0+abc")}, `"version":"1.0.0"`, "+abc", `"other_plugins":0`},
		{"prerelease kept", []PluginInfo{fp("a", "1.0.0-rc.1")}, `"version":"1.0.0-rc.1"`, "", `"other_plugins":0`},
		{"community kept as first-party flag decides", []PluginInfo{{Name: "mine", Version: "1.0.0"}}, "", `"name":"mine"`, `"other_plugins":1`},
		{"too long name", []PluginInfo{fp(strings.Repeat("a", 65), "1.0.0")}, "", strings.Repeat("a", 65), `"other_plugins":1`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFake()
			f.plugins = c.plugins
			got := build(t, f, Counts{}, nil)
			if c.listed != "" && !strings.Contains(got, c.listed) {
				t.Errorf("missing %s in %s", c.listed, got)
			}
			if c.unlisted != "" && strings.Contains(got, c.unlisted) {
				t.Errorf("unexpected %s in %s", c.unlisted, got)
			}
			if !strings.Contains(got, c.wantOther) {
				t.Errorf("want %s in %s", c.wantOther, got)
			}
		})
	}
}

func TestBuildDetailedManyPlugins(t *testing.T) {
	f := newFake()
	errs := map[string]int64{}
	for i := range 130 {
		name := fmt.Sprintf("plugin-%03d-%s", 129-i, strings.Repeat("x", 40))
		f.plugins = append(f.plugins, PluginInfo{Name: name, Version: "1.2.3-beta.10", Enabled: i%2 == 0, FirstParty: true})
		errs[name] = int64(i + 1)
	}
	got := build(t, f, Counts{PluginErrors: errs}, nil)
	if len(got) >= maxDetailedBytes {
		t.Fatalf("body is %d bytes", len(got))
	}
	// With 64-char names the 100 entries do not fit; the tail moves to the count.
	if n := strings.Count(got, `"enabled"`); n >= maxPlugins || !strings.Contains(got, fmt.Sprintf(`"other_plugins":%d`, 130-n)) {
		t.Fatalf("overflow not counted in other_plugins (%d listed): %s", n, got[len(got)-300:])
	}
	if !strings.Contains(got, "plugin-000-") || strings.Contains(got, "plugin-100-") {
		t.Fatal("expected the first names by sort order")
	}
	if !strings.Contains(got, `"other":`) {
		t.Fatal("errors of dropped plugins should be summed under other")
	}
}

func TestBuildDetailedClamps(t *testing.T) {
	f := newFake()
	f.failed, f.rolled = 5_000_000, -3
	win := testNow.Add(-20000 * time.Hour)
	got := build(t, f, Counts{Panics: 9_999_999, HTTP5xx: -1, RenderErrors: 1_000_000, MigrationFailed: 2_000_000}, &win)
	for _, s := range []string{`"since_hours":10000`, `"panics":1000000`, `"http_5xx":0`, `"render_errors":1000000`,
		`"update_failed":1000000`, `"update_rolled_back":0`, `"migration_failed":1000000`} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %s in %s", s, got)
		}
	}
	future := testNow.Add(time.Hour)
	if g := build(t, f, Counts{}, &future); !strings.Contains(g, `"since_hours":0`) {
		t.Errorf("future window should clamp to 0: %s", g)
	}
}

func TestCollapseErrorsKeyCap(t *testing.T) {
	var listed []detailedPlugin
	errs := map[string]int64{"stranger": 7}
	for i := range maxPlugins {
		name := fmt.Sprintf("p%03d", i)
		listed = append(listed, detailedPlugin{Name: name})
		errs[name] = 1
	}
	out := collapseErrors(errs, listed)
	if len(out) != maxErrorKeys {
		t.Fatalf("%d keys, want %d", len(out), maxErrorKeys)
	}
	if out[otherKey] != 8 { // the stranger and the one folded plugin
		t.Fatalf("other = %d, want 8", out[otherKey])
	}
}

func TestBuildDetailedFailures(t *testing.T) {
	cases := map[string]func(*fakeSources){
		"postgres 8":      func(f *fakeSources) { f.pg = 8 },
		"postgres 100":    func(f *fakeSources) { f.pg = 100 },
		"bad theme name":  func(f *fakeSources) { f.theme.Name = "Default" },
		"bad theme ver":   func(f *fakeSources) { f.theme.Version = "latest" },
		"update counts":   func(f *fakeSources) { f.updErr = errors.New("db") },
		"empty theme ver": func(f *fakeSources) { f.theme.Version = "" },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			mut(f)
			if b, err := BuildDetailed(context.Background(), BuildInput{Src: f, Now: testNow}); err == nil {
				t.Fatalf("want an error, got %s", b)
			}
		})
	}
	if _, err := BuildDetailed(context.Background(), BuildInput{Now: testNow}); err == nil {
		t.Fatal("nil sources should fail")
	}
}

func TestBuildDetailedThemeBuildStripped(t *testing.T) {
	f := newFake()
	f.theme.Version = "1.6.0+sha.1"
	if got := build(t, f, Counts{}, nil); !strings.Contains(got, `"version":"1.6.0"}`) {
		t.Fatalf("theme build metadata kept: %s", got)
	}
}

func TestBuildDetailedAgeUnknownIsZero(t *testing.T) {
	f := newFake()
	f.startErr = errors.New("no goose table")
	if got := build(t, f, Counts{}, nil); !strings.Contains(got, `"age_months":0`) {
		t.Fatalf("age: %s", got)
	}
}

func TestAgeBucket(t *testing.T) {
	for months, want := range map[int]int{0: 0, 1: 1, 2: 1, 3: 3, 5: 3, 6: 6, 11: 6, 12: 12, 23: 12, 24: 24, 36: 36, 100: 36, -4: 0} {
		if got := ageBucket(months); got != want {
			t.Errorf("ageBucket(%d) = %d, want %d", months, got, want)
		}
	}
}

func TestMonthsBetween(t *testing.T) {
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }
	for _, c := range []struct {
		start, now time.Time
		want       int
	}{
		{d(2026, 5, 9), d(2026, 11, 9), 6},
		{d(2026, 5, 10), d(2026, 11, 9), 5},
		{d(2025, 11, 9), d(2026, 11, 9), 12},
		{d(2027, 1, 1), d(2026, 11, 9), 0},
	} {
		if got := monthsBetween(c.start, c.now); got != c.want {
			t.Errorf("monthsBetween(%v,%v) = %d, want %d", c.start, c.now, got, c.want)
		}
	}
}

func TestValidateDetailedRejects(t *testing.T) {
	f := newFake()
	good := []byte(build(t, f, Counts{}, nil))
	for name, mut := range map[string]func(string) string{
		"extra key":  func(s string) string { return strings.Replace(s, `"level"`, `"extra":1,"level"`, 1) },
		"wrong case": func(s string) string { return strings.Replace(s, `"os"`, `"OS"`, 1) },
		"basic body": func(string) string { return `{"install":"x","version":"v","channel":"c"}` },
		"bad bucket": func(s string) string { return strings.Replace(s, `"age_months":6`, `"age_months":7`, 1) },
	} {
		if err := validateDetailed([]byte(mut(string(good)))); err == nil {
			t.Errorf("%s: validateDetailed accepted it", name)
		}
	}
}

func TestBuildDetailedHundredPluginsFit(t *testing.T) {
	f := newFake()
	for i := range maxPlugins {
		f.plugins = append(f.plugins, PluginInfo{Name: fmt.Sprintf("plugin-%03d", i), Version: "1.2.3", Enabled: true, FirstParty: true})
	}
	got := build(t, f, Counts{}, nil)
	if n := strings.Count(got, `"enabled"`); n != maxPlugins {
		t.Fatalf("%d listed, want %d", n, maxPlugins)
	}
}
