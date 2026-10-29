package admin

import (
	"os"
	"strings"
	"testing"
)

// The remembered sidebar is tested in scripts/admin-qa/unit/shelllogic.test.mjs; this checks
// admin.js still reads and writes it through those helpers.
func TestAdminJSUsesRememberedSidebar(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../web/admin/js/admin.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`from "@hm/shelllogic"`, `rememberedSidebar(safeStorage("local")`, `rememberSidebar(safeStorage("local")`, "photoFallback(", "photoFailed("} {
		if !strings.Contains(string(b), want) {
			t.Errorf("admin.js must call %s", want)
		}
	}
}
