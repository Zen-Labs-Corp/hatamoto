package portability

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ExportOptions control Export.
type ExportOptions struct {
	// MediaDir is the upload directory the media rows' paths are relative to.
	MediaDir string
	// NoSubmissions leaves out form submissions (visitor data).
	NoSubmissions bool
	// OnMissing, when set, is called with the relative path of each referenced
	// media file that is not on disk.
	OnMissing func(rel string)
	// Now is the archive's timestamp (tests); zero means time.Now.
	Now time.Time
}

// Export streams the site as a tar.gz to w. The database is read in one
// repeatable-read transaction, so the tables agree with each other; each table
// is spooled to a temp file (a tar entry needs its size first), so memory use
// does not grow with the site.
func Export(ctx context.Context, pool *pgxpool.Pool, w io.Writer, opts ExportOptions) (*Manifest, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var picked []table
	for _, t := range tables {
		if t.pii && opts.NoSubmissions {
			continue
		}
		if t.optional {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+t.name).Scan(&exists); err != nil {
				return nil, err
			}
			if !exists {
				continue
			}
		}
		picked = append(picked, t)
	}

	var schemaVersion int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&schemaVersion); err != nil {
		return nil, fmt.Errorf("read schema version (is the database migrated?): %w", err)
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	hdr := Header{Format: FormatName, Version: FormatVersion, CreatedAt: now.UTC(), SchemaVersion: schemaVersion, IncludeSubmission: !opts.NoSubmissions}
	for _, t := range picked {
		hdr.Tables = append(hdr.Tables, t.name)
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	if err := writeJSONEntry(tw, formatEntry, hdr, now); err != nil {
		return nil, err
	}

	man := &Manifest{Rows: map[string]int64{}}
	var mediaPaths []string
	for _, t := range picked {
		n, paths, err := exportTable(ctx, tx, tw, t, now)
		if err != nil {
			return nil, fmt.Errorf("export %s: %w", t.name, err)
		}
		man.Rows[t.name] = n
		mediaPaths = append(mediaPaths, paths...)
	}

	seen := map[string]bool{}
	for _, p := range mediaPaths {
		if seen[p] {
			continue
		}
		seen[p] = true
		size, err := addMediaFile(tw, opts.MediaDir, p, now)
		if err != nil {
			return nil, err
		}
		if size >= 0 {
			man.MediaFiles++
			man.MediaBytes += size
		} else {
			man.MediaMissing++
			if opts.OnMissing != nil {
				opts.OnMissing(p)
			}
		}
	}
	if err := writeJSONEntry(tw, manifestEntry, man, now); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return man, nil
}

func writeJSONEntry(tw *tar.Writer, name string, v any, mod time.Time) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), ModTime: mod}); err != nil {
		return err
	}
	_, err = tw.Write(b)
	return err
}

// exportTable writes data/<table>.jsonl (one JSON object per row) and returns
// the row count and, for the media table, the files its rows reference.
func exportTable(ctx context.Context, tx pgx.Tx, tw *tar.Writer, t table, mod time.Time) (int64, []string, error) {
	gen, err := generatedColumns(ctx, tx, t.name)
	if err != nil {
		return 0, nil, err
	}
	expr := "to_jsonb(t)"
	for _, c := range append(append([]string{}, t.drop...), gen...) {
		expr += fmt.Sprintf(" - '%s'", c) // column names come from catalog/our table, never the archive
	}
	rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s::text FROM public.%s t ORDER BY 1`, expr, t.name))
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()

	spool, err := os.CreateTemp("", "hatamoto-export-*.jsonl")
	if err != nil {
		return 0, nil, err
	}
	defer os.Remove(spool.Name())
	defer spool.Close()
	bw := bufio.NewWriter(spool)

	var (
		n     int64
		paths []string
	)
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return 0, nil, err
		}
		if t.name == "hatamoto_media" {
			ps, err := mediaPathsOf(line)
			if err != nil {
				return 0, nil, err
			}
			paths = append(paths, ps...)
		}
		bw.WriteString(line)
		bw.WriteByte('\n')
		n++
	}
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	if err := bw.Flush(); err != nil {
		return 0, nil, err
	}
	size, err := spool.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, nil, err
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return 0, nil, err
	}
	if err := tw.WriteHeader(&tar.Header{Name: dataPrefix + t.name + dataSuffix, Mode: 0o644, Size: size, ModTime: mod}); err != nil {
		return 0, nil, err
	}
	_, err = io.Copy(tw, spool)
	return n, paths, err
}

// generatedColumns lists the always-generated columns of a table (computed by
// the database, so neither exported nor imported).
func generatedColumns(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, tbl string) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 AND is_generated = 'ALWAYS'`, tbl)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// mediaPathsOf returns the stored file and the variant files of one media row.
func mediaPathsOf(line string) ([]string, error) {
	var m struct {
		StoragePath string `json:"storage_path"`
		Variants    []struct {
			Path string `json:"path"`
		} `json:"variants"`
	}
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return nil, err
	}
	out := []string{m.StoragePath}
	for _, v := range m.Variants {
		if v.Path != "" {
			out = append(out, v.Path)
		}
	}
	return out, nil
}

// addMediaFile copies one upload into the archive and returns its size, or -1
// when the file is missing on disk (the row is still exported; the import
// reports the gap through the manifest's file count).
func addMediaFile(tw *tar.Writer, dir, rel string, mod time.Time) (int64, error) {
	clean, err := cleanMediaPath(rel)
	if err != nil {
		return -1, err
	}
	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(clean)))
	if os.IsNotExist(err) {
		return -1, nil
	}
	if err != nil {
		return -1, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return -1, err
	}
	if !st.Mode().IsRegular() {
		return -1, nil
	}
	if err := tw.WriteHeader(&tar.Header{Name: mediaPrefix + clean, Mode: 0o644, Size: st.Size(), ModTime: mod}); err != nil {
		return -1, err
	}
	n, err := io.Copy(tw, io.LimitReader(f, st.Size()))
	if err == nil && n != st.Size() {
		err = fmt.Errorf("%s changed while exporting", clean)
	}
	return st.Size(), err
}
