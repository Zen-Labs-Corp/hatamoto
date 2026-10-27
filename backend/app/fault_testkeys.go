//go:build hatamoto_testkeys

package app

import (
	"os"
	"path/filepath"
	"strings"
)

// faultAfterMigrate makes the process die right after migrations, to exercise
// the supervisor's rollback. Test builds only. HATAMOTO_TEST_FAULT is
// "exit-after-migrate", optionally "exit-after-migrate@v1.0.1": then only the
// binary that runs from a directory named v1.0.1 (a staged release) fails, so
// the release it replaces keeps working with the same environment.
func faultAfterMigrate() {
	kind, only, _ := strings.Cut(os.Getenv("HATAMOTO_TEST_FAULT"), "@")
	if kind != "exit-after-migrate" {
		return
	}
	if exe, err := os.Executable(); only == "" || (err == nil && filepath.Base(filepath.Dir(exe)) == only) {
		os.Exit(1)
	}
}
