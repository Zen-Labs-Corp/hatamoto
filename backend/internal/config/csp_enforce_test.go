package config

import (
	"os"
	"testing"
)

func TestLookupEnvBool_CSPEnforce(t *testing.T) {
	cases := []struct {
		name    string
		set     bool
		val     string
		wantOn  bool
		wantSet bool
	}{
		{"unset", false, "", true, false},
		{"empty", true, "", true, false},
		{"false", true, "false", false, true},
		{"true", true, "true", true, true},
		{"garbage", true, "garbage", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.set {
				t.Setenv("CSP_ENFORCE", c.val)
			} else {
				// Unset for this test; Setenv registers the restore.
				t.Setenv("CSP_ENFORCE", "")
				_ = os.Unsetenv("CSP_ENFORCE")
			}
			on, set := lookupEnvBool("CSP_ENFORCE", true)
			if on != c.wantOn || set != c.wantSet {
				t.Fatalf("got (%v,%v), want (%v,%v)", on, set, c.wantOn, c.wantSet)
			}
		})
	}
}

func TestLoadCSPEnforceDefault(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef") // gitleaks:allow (test value)
	t.Setenv("CSP_ENFORCE", "")
	_ = os.Unsetenv("CSP_ENFORCE")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Security.CSPEnforce || cfg.Security.CSPEnforceSet {
		t.Fatalf("Security = %+v, want CSPEnforce=true, CSPEnforceSet=false", cfg.Security)
	}
}
