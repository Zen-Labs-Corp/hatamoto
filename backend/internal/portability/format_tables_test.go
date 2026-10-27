package portability

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/migrations"
)

// Every table the archive reads or checks must be created by a migration: a
// misspelt optional table is otherwise skipped silently (it once was).
func TestArchiveTablesExistInMigrations(t *testing.T) {
	var sql strings.Builder
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := fs.ReadFile(migrations.FS, e.Name())
		if err != nil {
			t.Fatal(err)
		}
		sql.Write(b)
		sql.WriteByte('\n')
	}
	names := map[string]bool{}
	for _, tb := range tables {
		names[tb.name] = true
	}
	for _, n := range contentTables {
		names[n] = true
	}
	for n := range names {
		re := regexp.MustCompile(`(?i)CREATE TABLE (IF NOT EXISTS )?(public\.)?` + regexp.QuoteMeta(n) + `\b`)
		if !re.MatchString(sql.String()) {
			t.Errorf("table %s is exported or checked but no migration creates it", n)
		}
	}
	if names["hatamoto_plugin_packages"] {
		t.Error("packages are not archived: hatamoto_plugin_packages must not come back")
	}
}
