package app

import (
	"bytes"
	"os"
	"path/filepath"
	"time"
)

// migrationMarkerName is the file in the data directory that counts failed
// migrations for the install stats. A failed migration aborts its transaction
// and usually ends in a rollback to the previous binary, so a database row
// cannot carry the count: the next boot imports the file instead.
const migrationMarkerName = "install-stats-migration-failed"

// recordMigrationFailure appends one line, a timestamp and nothing else (no
// error text, no version), to the marker file. It is best effort: a data
// directory that cannot be written is ignored, and the migration error that
// caused the call is never masked.
func recordMigrationFailure(dataDir string) {
	f, err := os.OpenFile(filepath.Join(dataDir, migrationMarkerName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(time.Now().UTC().Format(time.RFC3339) + "\n")
}

// readMigrationFailures returns the number of recorded failures; a missing or
// unreadable file counts as none. The caller adds the count to the database
// first and only then calls clearMigrationMarker.
func readMigrationFailures(dataDir string) int {
	b, err := os.ReadFile(filepath.Join(dataDir, migrationMarkerName))
	if err != nil {
		return 0
	}
	return bytes.Count(b, []byte("\n"))
}

// clearMigrationMarker deletes the marker file.
func clearMigrationMarker(dataDir string) {
	_ = os.Remove(filepath.Join(dataDir, migrationMarkerName))
}
