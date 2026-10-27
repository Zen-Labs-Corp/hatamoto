package server

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// TestAdminRouteTableGolden pins every /admin pattern and its handler. Each
// later slug that moves a screen to Go adds lines here; regenerate with
// UPDATE_GOLDEN=1 after a deliberate change.
func TestAdminRouteTableGolden(t *testing.T) {
	table, err := RouteTable()
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, r := range table {
		_, path, _ := strings.Cut(r.Pattern, " ")
		if path == "/admin" || strings.HasPrefix(path, "/admin/") {
			h := strings.TrimPrefix(r.Handler, "hatamoto.dev/core/internal/")
			lines = append(lines, r.Pattern+" -> "+h)
		}
	}
	sort.Strings(lines)
	got := strings.Join(lines, "\n") + "\n"
	const golden = "testdata/admin_routes.golden"
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("admin routes changed (UPDATE_GOLDEN=1 to accept):\n--- want\n%s--- got\n%s", want, got)
	}
}
