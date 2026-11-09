package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkerCountsAndClears(t *testing.T) {
	dir := t.TempDir()
	if n := readMigrationFailures(dir); n != 0 {
		t.Fatalf("empty dir read %d", n)
	}
	recordMigrationFailure(dir)
	recordMigrationFailure(dir)
	if n := readMigrationFailures(dir); n != 2 {
		t.Fatalf("read %d, want 2", n)
	}
	clearMigrationMarker(dir)
	if n := readMigrationFailures(dir); n != 0 {
		t.Fatalf("after clear read %d", n)
	}
}

func TestMarkerHoldsTimestampsOnly(t *testing.T) {
	dir := t.TempDir()
	recordMigrationFailure(dir)
	b, err := os.ReadFile(filepath.Join(dir, migrationMarkerName))
	if err != nil {
		t.Fatal(err)
	}
	if line := strings.TrimSpace(string(b)); len(line) != len("2006-01-02T15:04:05Z") || !strings.HasSuffix(line, "Z") {
		t.Fatalf("marker line %q is not a bare timestamp", line)
	}
	if fi, _ := os.Stat(filepath.Join(dir, migrationMarkerName)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

func TestMarkerUnwritableDirIsIgnored(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no", "such", "dir")
	recordMigrationFailure(missing) // must not panic
	if n := readMigrationFailures(missing); n != 0 {
		t.Fatalf("read %d from a missing dir", n)
	}
	clearMigrationMarker(missing)
}
