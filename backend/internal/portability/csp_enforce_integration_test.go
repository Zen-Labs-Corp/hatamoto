//go:build integration

package portability

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

// withSchemaVersion rewrites the schema version in the archive's header.
func withSchemaVersion(t *testing.T, archive []byte, version int64) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == formatEntry {
			var m map[string]any
			if err := jsonx.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			m["schema_version"] = version
			if b, err = jsonx.Marshal(m); err != nil {
				t.Fatal(err)
			}
			h.Size = int64(len(b))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// An archive from before csp_enforce must not start enforcing on import (NULL
// means enforce): it is pinned to report-only. A current archive keeps its
// value, including an untouched NULL, which means enforce.
func TestImportPinsCSPForArchiveWithoutIt(t *testing.T) {
	ctx := context.Background()
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
	export := func(value string) []byte {
		t.Helper()
		src := scratchPool(t)
		if _, err := src.Exec(ctx, `INSERT INTO hatamoto_site_config (company_name) VALUES ('x') ON CONFLICT DO NOTHING; UPDATE hatamoto_site_config SET csp_enforce = `+value); err != nil {
			t.Fatal(err)
		}
		return exportArchive(t, src, t.TempDir())
	}

	if v := read(export("true")); v == nil || !*v {
		t.Errorf("archive with true: csp_enforce = %v, want true", v)
	}
	null := export("NULL")
	if v := read(null); v != nil {
		t.Errorf("current archive with NULL: csp_enforce = %v, want NULL", *v)
	}
	old := withSchemaVersion(t, stripColumns(t, null, "csp_enforce"), cspEnforceMigration-1)
	if v := read(old); v == nil || *v {
		t.Errorf("older archive without the column: csp_enforce = %v, want false", v)
	}
}
