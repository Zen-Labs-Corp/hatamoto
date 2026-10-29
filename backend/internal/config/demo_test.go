package config

import (
	"os"
	"strings"
	"testing"
)

func TestDemoOffChangesNothing(t *testing.T) {
	t.Setenv("HATAMOTO_DEMO", "")
	t.Setenv("HATAMOTO_DEMO_LOGIN_EMAIL", "a@b.example")
	t.Setenv("HATAMOTO_DEMO_LOGIN_PASSWORD", "pw")
	t.Setenv("EMAIL_DISABLED", "")
	d, _ := LoadDemoConfig()
	if d.Enabled || d.ShowLogin() || d.LoginEmail != "" {
		t.Errorf("demo off: %+v", d)
	}
	if os.Getenv("EMAIL_DISABLED") != "" {
		t.Error("demo off must not touch EMAIL_DISABLED")
	}
	if _, refused := d.Refusal("POST", "/admin/settings/users"); refused {
		t.Fatal("refused with the demo off")
	}
}

func TestDemoOnLoadsLoginFromEnvAndSwitchesEmailOff(t *testing.T) {
	t.Setenv("HATAMOTO_DEMO", "1")
	t.Setenv("HATAMOTO_DEMO_LOGIN_EMAIL", "demo@example.test")
	t.Setenv("HATAMOTO_DEMO_LOGIN_PASSWORD", "pw-from-env")
	t.Setenv("EMAIL_DISABLED", "")
	d, _ := LoadDemoConfig()
	if !d.Enabled || !d.ShowLogin() || d.LoginEmail != "demo@example.test" || d.LoginPassword != "pw-from-env" {
		t.Errorf("demo on: %+v", d)
	}
	if os.Getenv("EMAIL_DISABLED") != "true" {
		t.Error("demo must switch email off")
	}
	t.Setenv("HATAMOTO_DEMO_LOGIN_PASSWORD", "")
	if func() bool { d, _ := LoadDemoConfig(); return d.ShowLogin() }() {
		t.Error("no password: no login on the page")
	}
}

// One case per refusal; the allowed rows keep the rules from over-matching.
func TestDemoRefusals(t *testing.T) {
	on := DemoConfig{Enabled: true}
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{"POST", "/admin/plugins/upload", true},
		{"POST", "/admin/plugins/unsigned", true},
		{"POST", "/admin/plugins/unsigned/off", true},
		{"POST", "/admin/settings/users", true},
		{"POST", "/admin/settings/users/invite", true},
		{"POST", "/admin/settings/users/7/reset", true},
		{"POST", "/admin/settings/users/7", true},
		{"POST", "/admin/settings/users/7/delete", true},
		{"POST", "/api/v1/users/", true},
		{"PATCH", "/api/v1/users/7", true},
		{"DELETE", "/api/v1/users/7", true},
		{"PATCH", "/api/v1/me", true},
		{"POST", "/admin/profile/password", true},
		{"POST", "/admin/forgot", true},
		{"POST", "/admin/reset", true},
		{"POST", "/admin/accept", true},
		{"POST", "/admin/settings/api-keys", true},
		{"POST", "/admin/settings/api-keys/7/regenerate", true},
		{"POST", "/api/v1/api-keys/", true},
		{"POST", "/api/v1/api-keys/7/regenerate", true},
		{"PATCH", "/api/v1/api-keys/7", true},
		{"POST", "/admin/settings/webhooks", true},
		{"POST", "/admin/settings/webhooks/7/test", true},
		{"POST", "/admin/settings/email", true},
		{"POST", "/admin/settings/email/test", true},
		{"POST", "/admin/setup/email", true},
		{"POST", "/admin/settings/spam", true},
		{"POST", "/admin/settings/import/wordpress", true},
		{"POST", "/admin/settings/updates", true},
		{"POST", "/admin/settings/updates/apply", true},
		{"POST", "/admin/settings/updates/check", true},
		{"POST", "/admin/settings/backups", true},
		{"POST", "/admin/settings/backups/7/delete", true},
		{"GET", "/admin/settings/backups/7/download", true},
		// an encoded slash inside an id reaches the guard as extra segments
		{"POST", "/admin/settings/api-keys/a/b/regenerate", true},
		{"POST", "/api/v1/api-keys/a/b/regenerate", true},
		{"PATCH", "/api/v1/api-keys/a/b", true},
		{"DELETE", "/api/v1/api-keys/a/b", false},
		// still allowed
		{"GET", "/admin/settings/backups", false},
		{"GET", "/admin/settings/users", false},
		{"GET", "/api/v1/users/", false},
		{"POST", "/admin/plugins/fetch", false},
		{"POST", "/admin/plugins/install", false},
		{"POST", "/admin/plugins/check-updates", false},
		{"GET", "/admin/plugins/security", false},
		{"POST", "/admin/settings/api-keys/7/revoke", false},
		{"POST", "/admin/entities", false},
		{"POST", "/admin/profile", false},
		{"POST", "/admin/login", false},
		{"POST", "/admin/settings/general/identity", false},
	} {
		msg, got := on.Refusal(tc.method, tc.path)
		if got != tc.want {
			t.Errorf("%s %s: refused = %v, want %v", tc.method, tc.path, got, tc.want)
		}
		if got && msg == "" {
			t.Errorf("%s %s: refusal without a message", tc.method, tc.path)
		}
		if _, off := (DemoConfig{}).Refusal(tc.method, tc.path); off {
			t.Errorf("%s %s: refused with the demo off", tc.method, tc.path)
		}
	}
}

func TestDemoFlagTable(t *testing.T) {
	for v, want := range map[string]bool{
		"": false, "  ": false, "0": false, "false": false, "FALSE": false, "no": false, "off": false, "Off": false,
		"1": true, "true": true, "TRUE": true, "yes": true, "Yes": true, "on": true, "ON": true, " on ": true,
	} {
		t.Setenv("HATAMOTO_DEMO", v)
		d, err := LoadDemoConfig()
		if err != nil || d.Enabled != want {
			t.Errorf("HATAMOTO_DEMO=%q: enabled=%v err=%v, want enabled=%v", v, d.Enabled, err, want)
		}
	}
	for _, v := range []string{"enabled", "2", "y", "n", "disabled", "tru", "-1"} {
		t.Setenv("HATAMOTO_DEMO", v)
		_, err := LoadDemoConfig()
		if err == nil || !strings.Contains(err.Error(), "invalid HATAMOTO_DEMO") {
			t.Errorf("HATAMOTO_DEMO=%q must refuse to start with a clear error, got %v", v, err)
		}
	}
}

func TestDemoDiskLimitsDefaultsAndOverrides(t *testing.T) {
	t.Setenv("HATAMOTO_DEMO", "1")
	d, _ := LoadDemoConfig()
	if d.MediaQuotaMB != 200 || d.WriteBudgetMB != 150 || d.WritesPerSecond != 2 || d.WriteBurst != 20 {
		t.Errorf("defaults: %+v", d)
	}
	t.Setenv("HATAMOTO_DEMO_MEDIA_QUOTA_MB", "50")
	t.Setenv("HATAMOTO_DEMO_WRITE_BUDGET_MB", "-3")
	d, _ = LoadDemoConfig()
	if d.MediaQuotaMB != 50 || d.WriteBudgetMB != 150 {
		t.Errorf("overrides: %+v", d)
	}
}
