//go:build integration

package portability

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json/jsontext"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/migrations"
)

// scratchPool makes an empty, migrated database from a superuser DATABASE_URL
// and drops it when the test ends.
func scratchPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_pt_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close(ctx)
		t.Skipf("cannot create a scratch database: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	prov, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prov.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

func exportArchive(t *testing.T, pool *pgxpool.Pool, mediaDir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := Export(context.Background(), pool, &buf, ExportOptions{MediaDir: mediaDir}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func archiveEntries(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = b
	}
}

func writeFile(t *testing.T, dir, rel string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A media row staged by an unpublished changeset run is left out of the
// export, together with its file; the live row is exported.
func TestExportSkipsStagedMedia(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	dir := t.TempDir()
	run := uuid.NewString()
	live, staged := "t1/live.png", "t1/staged/"+run+"/new.png"
	writeFile(t, dir, live)
	writeFile(t, dir, staged)
	for _, r := range []struct{ path, name string }{{live, "live.png"}, {staged, "new.png"}} {
		var runID any
		if r.path == staged {
			runID = run
		}
		if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_media (filename, original_filename, mime_type, file_size, storage_path, run_id)
			VALUES ($1, $1, 'image/png', 1, $2, $3)`, r.name, r.path, runID); err != nil {
			t.Fatal(err)
		}
	}

	entries := archiveEntries(t, exportArchive(t, pool, dir))
	rows := strings.Split(strings.TrimSpace(string(entries[dataPrefix+"hatamoto_media"+dataSuffix])), "\n")
	if len(rows) != 1 || !strings.Contains(rows[0], `"live.png"`) {
		t.Fatalf("media rows = %q; want only the live one", rows)
	}
	for name := range entries {
		if strings.Contains(name, "staged") {
			t.Errorf("archive holds staged file %s", name)
		}
	}
}

// An archive from a release without the origin and run_id columns imports; the
// rows come out owned by the owner.
func TestImportArchiveWithoutOriginIsOwner(t *testing.T) {
	ctx := context.Background()
	src := scratchPool(t)
	if _, err := src.Exec(ctx, `INSERT INTO hatamoto_menus (name, slug) VALUES ('Main', 'main')`); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec(ctx, `INSERT INTO hatamoto_redirects (source_path, target_url) VALUES ('/a', '/b')`); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec(ctx, `UPDATE hatamoto_menus SET origin = 'plugin-x'`); err != nil {
		t.Fatal(err)
	}
	old := stripColumns(t, exportArchive(t, src, t.TempDir()), "origin", "run_id")

	dst := scratchPool(t)
	if _, err := Import(ctx, dst, bytes.NewReader(old), ImportOptions{MediaDir: t.TempDir()}); err != nil {
		t.Fatalf("import: %v", err)
	}
	for _, tbl := range []string{"hatamoto_menus", "hatamoto_redirects"} {
		var n, owned int
		if err := dst.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE origin = 'owner') FROM `+tbl).Scan(&n, &owned); err != nil {
			t.Fatal(err)
		}
		if n == 0 || n != owned {
			t.Errorf("%s: %d rows, %d owned by owner; want all", tbl, n, owned)
		}
	}
}

// stripColumns rewrites an archive as an older release would have written it.
func stripColumns(t *testing.T, archive []byte, cols ...string) []byte {
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
		if strings.HasPrefix(h.Name, dataPrefix) {
			var lines [][]byte
			for _, l := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
				if len(l) == 0 {
					continue
				}
				var m map[string]jsontext.Value
				if err := jsonx.Unmarshal(l, &m); err != nil {
					t.Fatal(err)
				}
				for _, c := range cols {
					delete(m, c)
				}
				nl, _ := jsonx.Marshal(m)
				lines = append(lines, nl)
			}
			b = append(bytes.Join(lines, []byte("\n")), '\n')
			if len(lines) == 0 {
				b = nil
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

// Fonts keep their bytes in the row and must survive export and import.
func TestFontsRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := scratchPool(t)
	data := append([]byte("wOF2"), 0, 1, 2, 254, 255)
	if _, err := src.Exec(ctx, `INSERT INTO hatamoto_fonts (family, weight, style, source, subset, unicode_range, hash, data)
		VALUES ('Brand Sans', 700, 'italic', 'google', 'latin', 'U+0000-00FF', $1, $2)`, strings.Repeat("a", 64), data); err != nil {
		t.Fatal(err)
	}
	archive := exportArchive(t, src, t.TempDir())
	dst := scratchPool(t)
	if _, err := Import(ctx, dst, bytes.NewReader(archive), ImportOptions{MediaDir: t.TempDir()}); err != nil {
		t.Fatalf("import: %v", err)
	}
	var fam, style, rng string
	var weight int
	var got []byte
	if err := dst.QueryRow(ctx, `SELECT family, weight, style, unicode_range, data FROM hatamoto_fonts`).Scan(&fam, &weight, &style, &rng, &got); err != nil {
		t.Fatal(err)
	}
	if fam != "Brand Sans" || weight != 700 || style != "italic" || rng != "U+0000-00FF" || !bytes.Equal(got, data) {
		t.Errorf("font after import = %q %d %q %q % x", fam, weight, style, rng, got)
	}
}
