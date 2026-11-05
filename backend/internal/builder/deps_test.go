package builder

import (
	"os/exec"
	"strings"
	"testing"
)

// builder is a leaf package: render, entity and server depend on it, never the reverse (jsonx, stdlib only, is allowed).
func TestNoRenderOrEntityDeps(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.Contains(dep, "hatamoto.dev/core/internal/") && !strings.HasSuffix(dep, "/internal/builder") && !strings.HasSuffix(dep, "/internal/jsonx") {
			t.Errorf("builder imports internal package %s", dep)
		}
	}
}
