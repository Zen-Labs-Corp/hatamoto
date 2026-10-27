//go:build integration

package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/portability"
)

// dropPeople rewrites an archive into the shape a binary from before people
// wrote: no hatamoto_user_people file, no person type, no person entities.
func dropPeople(t *testing.T, raw []byte) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	tr := tar.NewReader(gz)
	put := func(h *tar.Header, b []byte) {
		h.Size = int64(len(b))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(b); err != nil {
			t.Fatal(err)
		}
	}
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
		switch h.Name {
		case "data/hatamoto_user_people.jsonl":
			continue
		case "format.json":
			var hdr map[string]any
			_ = json.Unmarshal(b, &hdr)
			var tables []any
			for _, n := range hdr["tables"].([]any) {
				if n != "hatamoto_user_people" {
					tables = append(tables, n)
				}
			}
			hdr["tables"] = tables
			b, _ = json.Marshal(hdr)
		case "manifest.json":
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			delete(m["rows"].(map[string]any), "hatamoto_user_people")
			b, _ = json.Marshal(m)
		case "data/hatamoto_entity_schemas.jsonl":
			var keep []string
			for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				if !strings.Contains(l, `"slug":"person"`) {
					keep = append(keep, l)
				}
			}
			b = []byte(strings.Join(keep, "\n") + "\n")
		}
		put(h, b)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// People survive export and import with their links, and an archive written
// before people existed gets its people made when it is imported.
func TestPeopleArchive_Integration(t *testing.T) {
	ctx := context.Background()
	_, src := scratchDB(t)
	_, dst := scratchDB(t)
	_, old := scratchDB(t)
	for _, p := range []*pgxpool.Pool{src, dst, old} {
		if err := migrateUp(ctx, p, quietLogger()); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := src.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO hatamoto_users (id,email,password_hash,role,name) VALUES (gen_random_uuid(),'owner@example.test','x','owner','Jane Doe')`)
	exec(`INSERT INTO hatamoto_authors (slug,name,bio) VALUES ('jane-doe','Jane Doe','Writes things')`)
	exec(`INSERT INTO hatamoto_entities (schema_id,name,slug,status,published_at,author_id)
		SELECT s.id,'Hello','hello','published',now(),(SELECT id FROM hatamoto_authors) FROM hatamoto_entity_schemas s WHERE s.slug='post'`)

	// Before people: authors only. Export that site, then strip the people-only parts.
	export := func() []byte {
		var buf bytes.Buffer
		if _, err := portability.Export(ctx, src, &buf, portability.ExportOptions{MediaDir: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	legacy := dropPeople(t, export())
	res, err := portability.Import(ctx, old, bytes.NewReader(legacy), portability.ImportOptions{MediaDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if res.People != 1 {
		t.Errorf("people made for the old archive = %d, want 1", res.People)
	}
	var byline int
	if err := old.QueryRow(ctx, `
		SELECT count(*) FROM hatamoto_entities e JOIN hatamoto_entities p ON p.id = e.byline_person_id
		WHERE e.slug = 'hello' AND p.slug = 'jane-doe'`).Scan(&byline); err != nil || byline != 1 {
		t.Errorf("post credits the new person: %d (%v)", byline, err)
	}
	var linked int
	_ = old.QueryRow(ctx, `SELECT count(*) FROM hatamoto_user_people`).Scan(&linked)
	if linked != 1 {
		t.Errorf("user links after backfill = %d, want 1", linked)
	}

	// With people: backfill the source, export, import: same people, same links, nothing made twice.
	sdb := stdlib.OpenDBFromPool(src)
	defer sdb.Close()
	if err := person.Backfill(ctx, sdb); err != nil {
		t.Fatal(err)
	}
	res, err = portability.Import(ctx, dst, bytes.NewReader(export()), portability.ImportOptions{MediaDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if res.People != 0 || res.Rows["hatamoto_user_people"] != 1 {
		t.Errorf("import result = people %d, link rows %d", res.People, res.Rows["hatamoto_user_people"])
	}
	for _, q := range []string{
		`SELECT string_agg(user_id::text||'|'||entity_id::text, ';' ORDER BY user_id) FROM hatamoto_user_people`,
		`SELECT string_agg(id::text||'|'||coalesce(byline_person_id::text,'-')||'|'||byline_org, ';' ORDER BY id) FROM hatamoto_entities`,
	} {
		var a, b string
		_ = src.QueryRow(ctx, q).Scan(&a)
		_ = dst.QueryRow(ctx, q).Scan(&b)
		if a != b || a == "" {
			t.Errorf("mismatch for %.50s\n src: %s\n dst: %s", q, a, b)
		}
	}
}
