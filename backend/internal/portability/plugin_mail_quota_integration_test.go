//go:build integration

package portability

import (
	"bytes"
	"context"
	"testing"
)

// The owner-set mail quota lives in hatamoto_plugin_grants.mail_per_hour and
// travels with the grants row: export is column-generic, so the value
// round-trips, and an archive from a release without the column imports with
// the default (NULL).
func TestPluginMailQuotaRoundTrips(t *testing.T) {
	ctx := context.Background()
	src := scratchPool(t)
	if _, err := src.Exec(ctx, `INSERT INTO hatamoto_plugin_grants (plugin, permissions, mail_per_hour) VALUES ('with-quota', '{mail.send}', 3600), ('default-quota', '{mail.send}', NULL)`); err != nil {
		t.Fatal(err)
	}
	archive := exportArchive(t, src, t.TempDir())

	read := func(archive []byte) map[string]*int {
		t.Helper()
		dst := scratchPool(t)
		if _, err := Import(ctx, dst, bytes.NewReader(archive), ImportOptions{MediaDir: t.TempDir()}); err != nil {
			t.Fatalf("import: %v", err)
		}
		rows, err := dst.Query(ctx, `SELECT plugin, mail_per_hour FROM hatamoto_plugin_grants`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := map[string]*int{}
		for rows.Next() {
			var p string
			var n *int
			if err := rows.Scan(&p, &n); err != nil {
				t.Fatal(err)
			}
			got[p] = n
		}
		return got
	}

	got := read(archive)
	if n := got["with-quota"]; n == nil || *n != 3600 {
		t.Errorf("with-quota = %v, want 3600", n)
	}
	if n, ok := got["default-quota"]; !ok || n != nil {
		t.Errorf("default-quota = %v (present %v), want NULL", n, ok)
	}

	old := read(stripColumns(t, archive, "mail_per_hour"))
	if len(old) != 2 || old["with-quota"] != nil || old["default-quota"] != nil {
		t.Errorf("archive without the column: %v, want both NULL", old)
	}
}
