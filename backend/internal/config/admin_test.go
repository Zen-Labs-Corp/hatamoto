package config

import (
	"strings"
	"testing"
	"time"
)

func TestTrustedProxiesParsing(t *testing.T) {
	t.Setenv("HATAMOTO_TRUSTED_PROXIES", "100.64.0.0/10, 203.0.113.7 ,")
	a := loadAdminConfig()
	if len(a.TrustedProxies) != 2 || a.TrustedProxies[1] != "203.0.113.7/32" || a.Validate() != nil {
		t.Fatalf("%v %v", a.TrustedProxies, a.Validate())
	}
	if (AdminConfig{TrustedProxies: []string{"nonsense"}}).Validate() == nil {
		t.Error("a non-CIDR must be rejected")
	}
}

func TestSetupCodeValidation(t *testing.T) {
	for code, ok := range map[string]bool{
		"":                 true, // generated per boot
		"short-code":       false,
		"aaaaaaaaaaaa":     false,
		"abababababab":     false,
		"abcabcabcabc":     false,
		"1212121212121212": false,
		"abcdabcdabcdabcd": false,
		"112233112233":     false,
		"elevenchars":      false,
		"aA -aA -aA -a":    false, // normalises to AAAAAAA
		"ab-cd-ef-gh-x":    false, // only 9 characters once normalised
		"aA-bB-cC-dD-eE-f": false, // 11 characters once normalised
		"MyLaunch-Code-26": true,
		"correct-horse-x7": true,
		"7f3k-q9xd-p2mw":   true,
	} {
		err := AdminConfig{SetupCode: code}.CheckSetupCode()
		if (err == nil) != ok {
			t.Errorf("code %q: err = %v, want ok=%v", code, err, ok)
		}
	}
}

// The setup-code rule is not part of Validate: an upgraded install with users
// and an old short code must still load its config.
func TestShortSetupCodeDoesNotFailValidate(t *testing.T) {
	if err := (AdminConfig{SetupCode: "short"}).Validate(); err != nil {
		t.Fatalf("Validate = %v", err)
	}
}

func TestAdminDevFlag(t *testing.T) {
	t.Setenv("HATAMOTO_DEV", "")
	if loadAdminConfig().Dev {
		t.Error("Dev must be off unless HATAMOTO_DEV=1")
	}
	t.Setenv("HATAMOTO_DEV", "true")
	if loadAdminConfig().Dev {
		t.Error("only the value 1 turns Dev on")
	}
	t.Setenv("HATAMOTO_DEV", "1")
	if !loadAdminConfig().Dev {
		t.Error("HATAMOTO_DEV=1 must turn Dev on")
	}
}

func TestAdminPathEnvValid(t *testing.T) {
	t.Setenv("HATAMOTO_ADMIN_PATH", " /My-Place ")
	a := loadAdminConfig()
	if a.Path != "/my-place" || a.Validate() != nil {
		t.Fatalf("Path = %q, Validate = %v", a.Path, a.Validate())
	}
	t.Setenv("HATAMOTO_ADMIN_PATH", "")
	if a := loadAdminConfig(); a.Path != "" || a.Validate() != nil {
		t.Errorf("unset: Path = %q, Validate = %v", a.Path, a.Validate())
	}
}

func TestAdminPathEnvMalformedFailsValidate(t *testing.T) {
	for _, bad := range []string{"my-place", "/a/b", "/abc", "/ab_cd", "/-abcd"} {
		t.Setenv("HATAMOTO_ADMIN_PATH", bad)
		err := loadAdminConfig().Validate()
		if err == nil || !strings.Contains(err.Error(), "HATAMOTO_ADMIN_PATH") || !strings.Contains(err.Error(), bad) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
	t.Setenv("HATAMOTO_ADMIN_PATH", "x")
	want := `HATAMOTO_ADMIN_PATH: "x" is not a valid admin address (one segment of 4 to 64 lowercase letters, digits or dashes, like /my-place)`
	if err := loadAdminConfig().Validate(); err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

func TestNotFoundDefaults(t *testing.T) {
	t.Setenv("HATAMOTO_NOTFOUND_PER_MIN", "")
	t.Setenv("HATAMOTO_NOTFOUND_BLOCK", "")
	a := loadAdminConfig()
	if a.NotFoundPerMin != 60 || a.NotFoundBlock != 2*time.Minute || a.Validate() != nil {
		t.Errorf("%d %v %v", a.NotFoundPerMin, a.NotFoundBlock, a.Validate())
	}
	t.Setenv("HATAMOTO_NOTFOUND_PER_MIN", "10")
	t.Setenv("HATAMOTO_NOTFOUND_BLOCK", "30s")
	if a := loadAdminConfig(); a.NotFoundPerMin != 10 || a.NotFoundBlock != 30*time.Second || a.Validate() != nil {
		t.Errorf("%d %v %v", a.NotFoundPerMin, a.NotFoundBlock, a.Validate())
	}
}

func TestNotFoundBadValues(t *testing.T) {
	for _, tc := range []struct{ env, val string }{
		{"HATAMOTO_NOTFOUND_PER_MIN", "-1"},
		{"HATAMOTO_NOTFOUND_PER_MIN", "abc"},
		{"HATAMOTO_NOTFOUND_PER_MIN", "1.5"},
		{"HATAMOTO_NOTFOUND_BLOCK", "soon"},
		{"HATAMOTO_NOTFOUND_BLOCK", "-1m"},
		{"HATAMOTO_NOTFOUND_BLOCK", "5"},
		{"HATAMOTO_NOTFOUND_BLOCK", "0"},
		{"HATAMOTO_NOTFOUND_BLOCK", "0s"},
		{"HATAMOTO_NOTFOUND_BLOCK", "500ms"},
	} {
		t.Setenv(tc.env, tc.val)
		err := loadAdminConfig().Validate()
		if err == nil || !strings.Contains(err.Error(), tc.env) {
			t.Errorf("%s=%q: err = %v", tc.env, tc.val, err)
		}
		t.Setenv(tc.env, "")
	}
}

func TestNotFoundBlockErrorNamesPerMin(t *testing.T) {
	t.Setenv("HATAMOTO_NOTFOUND_BLOCK", "0")
	err := loadAdminConfig().Validate()
	if err == nil || !strings.Contains(err.Error(), "HATAMOTO_NOTFOUND_PER_MIN=0") {
		t.Errorf("err = %v", err)
	}
	t.Setenv("HATAMOTO_NOTFOUND_BLOCK", "1s")
	if err := loadAdminConfig().Validate(); err != nil {
		t.Errorf("1s refused: %v", err)
	}
}

func TestNotFoundZeroDisables(t *testing.T) {
	t.Setenv("HATAMOTO_NOTFOUND_PER_MIN", "0")
	a := loadAdminConfig()
	if a.NotFoundPerMin != 0 || a.Validate() != nil {
		t.Errorf("%d %v", a.NotFoundPerMin, a.Validate())
	}
}
