package server

import (
	"encoding/hex"
	"testing"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/importjob"
)

// An existing install keeps its flash, preview and import-credential keys only
// while the same secret value derives the same bytes. The constants were
// computed with the pre-v1-15 code (HMAC-SHA256 keyed by JWT_SECRET) for this
// secret; a changed HMAC label or config field fails here before it breaks
// every sealed credential on a live site.
const (
	pinnedSecret     = "0123456789abcdef0123456789abcdef-pinned"                          // gitleaks:allow (test vector)
	pinnedFlashKey   = "36aeb37e8517c6e7f4d4fad3982d1ac9e634c3e1b82760db3110362c1b98cf3d" // gitleaks:allow (test vector)
	pinnedPreviewKey = "f250759314b0c2d38c6c9c3215699b75140669184bf0b5b74785660b69086c58" // gitleaks:allow (test vector)
	pinnedImportKey  = "1a66622d03a37866768217fcdce8facf54ff955403ad0882f74ae965564d69ec" // gitleaks:allow (test vector)
)

func TestDerivedKeysArePinnedToTheSecret(t *testing.T) {
	for _, name := range []string{"JWT_SECRET", "HATAMOTO_SECRET"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("JWT_SECRET", "")
			t.Setenv("HATAMOTO_SECRET", "")
			t.Setenv(name, pinnedSecret)
			t.Setenv("ENVIRONMENT", "development")
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			s := &Server{cfg: cfg}
			for label, c := range map[string][2]string{
				"flash":   {hex.EncodeToString(s.adminFlashKey()), pinnedFlashKey},
				"preview": {hex.EncodeToString(previewKey(cfg.Secret)), pinnedPreviewKey},
				"import":  {hex.EncodeToString(importjob.DeriveKey(cfg.Secret)), pinnedImportKey},
			} {
				if c[0] != c[1] {
					t.Errorf("%s key = %s, want %s", label, c[0], c[1])
				}
			}
		})
	}
}
