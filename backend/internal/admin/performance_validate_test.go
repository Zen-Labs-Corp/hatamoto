package admin

import (
	"net/url"
	"testing"
	"time"
)

func perfForm(kv ...string) url.Values {
	v := url.Values{"enabled": {"on"}, "ttl": {"3600"}, "max_mb": {""}, "preload": {"off"}, "interval": {"3600"}}
	for i := 0; i < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v
}

func TestValidatePerformanceTTL(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		ttl  string
		want int // 0 = rejected
	}{
		{"29", 0}, {"30", 30}, {"86400", 86400}, {"86401", 86401}, {"2592000", 2592000}, {"2592001", 0}, {"soon", 0}, {"", 0}, {"3600", 3600},
	} {
		got, errs := validatePerformance(perfForm("ttl", tc.ttl), PageCacheSettings{Enabled: true})
		if tc.want == 0 {
			if len(errs) != 1 || errs[0].ID != "ttl" {
				t.Errorf("ttl %q: errs = %+v, want one ttl error", tc.ttl, errs)
			}
			continue
		}
		if len(errs) != 0 || got.TTLSeconds != tc.want {
			t.Errorf("ttl %q: got %d errs %+v", tc.ttl, got.TTLSeconds, errs)
		}
	}
}

func TestValidatePerformanceMaxMB(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"", 0, false}, {"0", 0, true}, {"-1", 0, true}, {"1", 1, false},
		{"300", 300, false}, {"102400", 102400, false}, {"102401", 0, true}, {"abc", 0, true},
	} {
		got, errs := validatePerformance(perfForm("max_mb", tc.in), PageCacheSettings{Enabled: true})
		if tc.wantErr {
			if len(errs) != 1 || errs[0].ID != "max-mb" {
				t.Errorf("max %q: errs = %+v", tc.in, errs)
			}
			continue
		}
		if len(errs) != 0 || got.MaxMB != tc.want {
			t.Errorf("max %q: got %d errs %+v", tc.in, got.MaxMB, errs)
		}
	}
}

func TestValidatePerformanceInterval(t *testing.T) {
	t.Parallel()

	stored := PageCacheSettings{Enabled: true, PreloadIntervalSeconds: 600}
	for _, tc := range []struct {
		in      string
		wantErr bool
	}{
		{"900", false}, {"86400", false}, {"901", true}, {"", true}, {"600", false}, // 600 is the stored value
	} {
		got, errs := validatePerformance(perfForm("preload", "on", "interval", tc.in), stored)
		if tc.wantErr != (len(errs) > 0) {
			t.Errorf("interval %q: errs = %+v", tc.in, errs)
		}
		if !tc.wantErr && !got.PreloadEnabled {
			t.Errorf("interval %q: preload not enabled", tc.in)
		}
	}
	// A bad interval is ignored while preload is Off.
	if _, errs := validatePerformance(perfForm("preload", "off", "interval", "7"), stored); len(errs) != 0 {
		t.Errorf("preload off: errs = %+v", errs)
	}
}

func TestValidatePerformanceOffKeepsStored(t *testing.T) {
	t.Parallel()

	stored := PageCacheSettings{Enabled: true, TTLSeconds: 600, MaxMB: 300, PreloadEnabled: true, PreloadIntervalSeconds: 3600}
	// Disabled controls are not posted.
	got, errs := validatePerformance(url.Values{"enabled": {"off"}, "ttl": {"7200"}}, stored)
	if len(errs) != 0 {
		t.Fatalf("errs = %+v", errs)
	}
	want := stored
	want.Enabled, want.TTLSeconds = false, 7200
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestValidatePerformanceAbsentVersusEmpty(t *testing.T) {
	t.Parallel()

	stored := PageCacheSettings{TTLSeconds: 3600, MaxMB: 500, PreloadEnabled: true, PreloadIntervalSeconds: 7200, PreloadMode: "missing"}
	got, errs := validatePerformance(url.Values{"enabled": {"on"}, "ttl": {"3600"}}, stored)
	want := stored
	want.Enabled = true
	if len(errs) != 0 || got != want {
		t.Fatalf("absent keys: got %+v errs %+v, want %+v", got, errs, want)
	}
	got, errs = validatePerformance(url.Values{"enabled": {"on"}, "ttl": {"3600"}, "max_mb": {""}}, stored)
	if len(errs) != 0 || got.MaxMB != 0 || !got.PreloadEnabled || got.PreloadIntervalSeconds != 7200 {
		t.Fatalf("empty max_mb: got %+v errs %+v", got, errs)
	}
}

func TestValidatePerformanceRadios(t *testing.T) {
	t.Parallel()

	_, errs := validatePerformance(perfForm("enabled", "maybe", "preload", ""), PageCacheSettings{})
	if len(errs) != 1 || errs[0].ID != "page-cache" {
		t.Fatalf("errs = %+v", errs)
	}
}

func TestPerformancePresets(t *testing.T) {
	t.Parallel()

	if ttlPresets[0] != 5*time.Minute || ttlPresets[len(ttlPresets)-1] != 30*24*time.Hour {
		t.Errorf("ttl presets = %v", ttlPresets)
	}
	if intervalPresets[0] != 15*time.Minute || intervalPresets[len(intervalPresets)-1] != 30*24*time.Hour {
		t.Errorf("interval presets = %v", intervalPresets)
	}
}

func TestValidatePerformancePreloadNeedsInterval(t *testing.T) {
	t.Parallel()

	stored := PageCacheSettings{Enabled: true}
	form := url.Values{"enabled": {"on"}, "ttl": {"300"}, "preload": {"on"}}
	if _, errs := validatePerformance(form, stored); len(errs) != 1 || errs[0].ID != "preload-interval" {
		t.Fatalf("absent interval: errs = %+v", errs)
	}
	form.Set("interval", "0")
	if _, errs := validatePerformance(form, stored); len(errs) != 1 || errs[0].ID != "preload-interval" {
		t.Fatalf("interval 0: errs = %+v", errs)
	}
	// A stored interval is kept when the key is absent.
	stored.PreloadIntervalSeconds = 900
	form.Del("interval")
	if got, errs := validatePerformance(form, stored); len(errs) != 0 || got.PreloadIntervalSeconds != 900 {
		t.Fatalf("stored interval: got %+v errs %+v", got, errs)
	}
}

// A switch posts its on value first and the hidden off sentinel after it, so a
// checked switch reads on and an unchecked one (sentinel only) reads off.
func TestValidatePerformanceSwitchSentinel(t *testing.T) {
	t.Parallel()

	stored := PageCacheSettings{Enabled: true, TTLSeconds: 600, PreloadEnabled: true, PreloadIntervalSeconds: 3600}
	off, errs := validatePerformance(url.Values{"enabled": {"on"}, "preload": {"off"}, "ttl": {"3600"}}, stored)
	if len(errs) != 0 || !off.Enabled || off.PreloadEnabled {
		t.Fatalf("unchecked switches: %+v %+v", off, errs)
	}
	on, errs := validatePerformance(url.Values{"enabled": {"on", "off"}, "preload": {"on", "off"}, "ttl": {"3600"}, "interval": {"3600"}}, stored)
	if len(errs) != 0 || !on.Enabled || !on.PreloadEnabled {
		t.Fatalf("checked switches: %+v %+v", on, errs)
	}
}

func TestValidatePerformanceModeAndAfterChanges(t *testing.T) {
	t.Parallel()

	stored := PageCacheSettings{Enabled: true, PreloadMode: "all", PreloadAfterChanges: true}
	got, errs := validatePerformance(perfForm(), stored)
	if len(errs) != 0 || got.PreloadMode != "all" || !got.PreloadAfterChanges {
		t.Fatalf("absent keys must keep stored: %+v %+v", got, errs)
	}
	got, errs = validatePerformance(perfForm("mode", "missing", "after_changes", "off"), stored)
	if len(errs) != 0 || got.PreloadMode != "missing" || got.PreloadAfterChanges {
		t.Fatalf("posted: %+v %+v", got, errs)
	}
	got, _ = validatePerformance(perfForm(), PageCacheSettings{Enabled: true})
	if got.PreloadMode != "missing" {
		t.Fatalf("empty stored mode must read as missing, got %q", got.PreloadMode)
	}
	_, errs = validatePerformance(perfForm("mode", "some", "after_changes", "maybe"), stored)
	if len(errs) != 2 || errs[0].ID != "preload-mode" || errs[1].ID != "preload-after-changes" {
		t.Fatalf("bad values: %+v", errs)
	}
}

func TestValidatePerformanceLongLifetimes(t *testing.T) {
	t.Parallel()

	for ttl, ok := range map[string]bool{"172800": true, "432000": true, "2592000": true, "2592001": false} {
		_, errs := validatePerformance(perfForm("ttl", ttl), PageCacheSettings{})
		if (len(errs) == 0) != ok {
			t.Errorf("ttl %s: errs %+v", ttl, errs)
		}
	}
	for _, ivl := range []string{"172800", "432000", "2592000"} {
		_, errs := validatePerformance(perfForm("preload", "on", "interval", ivl), PageCacheSettings{})
		if len(errs) != 0 {
			t.Errorf("interval %s: %+v", ivl, errs)
		}
	}
}
