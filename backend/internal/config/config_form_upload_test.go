package config

import (
	"strings"
	"testing"
)

func TestLoadFormUploadMaxTotalMB(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@localhost/x")
	t.Setenv("HATAMOTO_SECRET", strings.Repeat("s", 32))
	for _, tc := range []struct {
		env       string
		wantMB    int
		wantBytes int64
	}{
		{"", 1024, 1024 << 20},
		{"5", 5, 5 << 20},
		{"0", 0, 1024 << 20},
		{"-3", -3, 1024 << 20},
		{"abc", 1024, 1024 << 20},
	} {
		t.Setenv("FORM_UPLOAD_MAX_TOTAL_MB", tc.env)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.Server.FormUploadMaxTotalMB; got != tc.wantMB {
			t.Errorf("%q: MB = %d, want %d", tc.env, got, tc.wantMB)
		}
		if got := cfg.Server.FormUploadMaxTotalBytes(); got != tc.wantBytes {
			t.Errorf("%q: bytes = %d, want %d", tc.env, got, tc.wantBytes)
		}
	}
}
