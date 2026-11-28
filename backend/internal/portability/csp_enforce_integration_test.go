//go:build integration

package portability

import (
	"bytes"
	"context"
	"testing"
)

// An archive from a release without csp_enforce must not start enforcing on
// import (NULL means enforce): it is pinned to report-only. A stored choice
// travels with the archive.
func TestImportPinsCSPForArchiveWithoutIt(t *testing.T) {
	ctx := context.Background()
	src := scratchPool(t)
	if _, err := src.Exec(ctx, `INSERT INTO hatamoto_site_config (company_name, csp_enforce) VALUES ('x', true) ON CONFLICT DO NOTHING; UPDATE hatamoto_site_config SET csp_enforce = true`); err != nil {
		t.Fatal(err)
	}
	archive := exportArchive(t, src, t.TempDir())

	read := func(archive []byte) *bool {
		t.Helper()
		dst := scratchPool(t)
		if _, err := Import(ctx, dst, bytes.NewReader(archive), ImportOptions{MediaDir: t.TempDir()}); err != nil {
			t.Fatalf("import: %v", err)
		}
		var v *bool
		if err := dst.QueryRow(ctx, `SELECT csp_enforce FROM hatamoto_site_config`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v := read(archive); v == nil || !*v {
		t.Errorf("archive with true: csp_enforce = %v, want true", v)
	}
	if v := read(stripColumns(t, archive, "csp_enforce")); v == nil || *v {
		t.Errorf("archive without the column: csp_enforce = %v, want false", v)
	}
}
