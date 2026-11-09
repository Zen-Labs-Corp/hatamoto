package config

import (
	"strings"
	"testing"
)

func TestMediaStorageConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@localhost/x")
	t.Setenv("HATAMOTO_SECRET", strings.Repeat("s", 32))
	for _, tc := range []struct {
		env, want string
		bad       bool
	}{{"", "local", false}, {"local", "local", false}, {"S3", "s3", false}, {"gcs", "gcs", true}} {
		t.Setenv("MEDIA_STORAGE", tc.env)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Server.MediaStorage != tc.want {
			t.Errorf("%q: MediaStorage = %q, want %q", tc.env, cfg.Server.MediaStorage, tc.want)
		}
		if err := cfg.Validate(); (err != nil) != tc.bad {
			t.Errorf("%q: Validate() = %v", tc.env, err)
		}
	}
}
