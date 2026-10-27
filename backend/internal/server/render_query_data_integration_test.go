//go:build integration

package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/testutil"
)

// Query offset/exclude/updated_at, the batch variant prefetch and the
// statement budget of a 12-item query (B7):
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run RenderQueryData
func TestRenderQueryDataIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping query integration test (needs a migrated Postgres)")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "qd-it")
	t.Setenv("HATAMOTO_SITE_NAME", "QD IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "QD IT")
	t.Setenv("PUBLIC_BASE_URL", "https://qd-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pcfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	var counter testutil.QueryCounter
	pcfg.ConnConfig.Tracer = &counter
	pcfg.MaxConns, pcfg.MinConns = 6, 1
	db, err := database.NewWithConfig(ctx, pcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	sfx := uuid.NewString()[:6]
	schema := "qd-" + sfx
	base := "/qd-" + sfx
	schemaID, authorID := uuid.New(), uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'QD Posts',$2,'{"type":"object"}',$3)`,
		schemaID, schema, fmt.Sprintf(`{"url_pattern":"%s/{path}","archive_path":"%s"}`, base, base))
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		SELECT $1, id, 'Quinn', $2::text, 'published', now(), '{"public":true}'::jsonb
		FROM hatamoto_entity_schemas WHERE slug = 'person'`, authorID, "qda-"+sfx)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_media WHERE storage_path LIKE $1`, "qd-"+sfx+"%")
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE id=$1`, authorID)
	})

	// 14 posts, each with its own image, an author, and a parent chain (p1 is
	// the parent of all, so the URLs are nested: base/p1/pN).
	ids := make([]uuid.UUID, 15)
	for i := 1; i <= 14; i++ {
		ids[i] = uuid.New()
		var parent any
		if i > 1 {
			parent = ids[1]
		}
		img := fmt.Sprintf("/uploads/qd-%s-%02d.jpg", sfx, i)
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,updated_at,attributes,byline_person_id,parent_id)
			VALUES ($1,$2,$3,$4,'published', now() - ($5 || ' hours')::interval, now() - ($6 || ' hours')::interval, $7::jsonb,$8,$9)`,
			ids[i], schemaID, fmt.Sprintf("Post %02d", i), fmt.Sprintf("p%d", i), fmt.Sprint(i), fmt.Sprint(20-i),
			fmt.Sprintf(`{"featured_image":%q}`, img), authorID, parent)
		exec(`INSERT INTO hatamoto_media (filename,original_filename,mime_type,file_size,storage_path,public_url,width,height,variants)
			VALUES ($1,$1,'image/jpeg',1,$2,$3,2000,1500,$4::jsonb)`, fmt.Sprintf("qd-%s-%02d.jpg", sfx, i), fmt.Sprintf("qd-%s-%02d", sfx, i),
			"https://cdn.example.test"+img,
			`[{"size":"medium","url":"/uploads/m.jpg","width":600,"height":400},{"size":"thumbnail","url":"/uploads/t.jpg","width":150,"height":150}]`)
	}

	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d := newRenderData(srv)
	names := func(r render.QueryResult) string {
		var out []string
		for _, it := range r.Items {
			out = append(out, strings.TrimPrefix(it.Name, "Post "))
		}
		return strings.Join(out, ",")
	}

	// Offset, exclude, updated_at (post N updated N hours... 20-N hours ago: post 14 oldest).
	q := render.QuerySpec{Schema: schema, Order: "published_at", Limit: 3, Page: 1}
	if r := d.Query(q); names(r) != "14,13,12" || r.Pages != 5 {
		t.Errorf("base = %s pages=%d", names(r), r.Pages)
	}
	q.Order = "-published_at"
	q.Offset = 2
	if r := d.Query(q); names(r) != "03,04,05" || r.Total != 14 || r.Pages != 4 || r.Page != 1 {
		t.Errorf("offset = %s total=%d pages=%d", names(r), r.Total, r.Pages)
	}
	q.Page = 99 // past the end holds the last page: rows 12 of 14 after skipping 2 -> page 4 has 1,..
	if r := d.Query(q); r.Page != 4 || r.Pages != 4 || names(r) != "12,13,14" {
		t.Errorf("last page = %s page=%d pages=%d", names(r), r.Page, r.Pages)
	}
	q.Offset, q.Page, q.Limit = 20, 1, 3 // everything skipped
	if r := d.Query(q); len(r.Items) != 0 || r.Pages != 1 || r.Page != 1 {
		t.Errorf("all skipped = %s pages=%d page=%d", names(r), r.Pages, r.Page)
	}
	q = render.QuerySpec{Schema: schema, Order: "-published_at", Limit: 3, Page: 1, ExcludeID: ids[1].String()}
	if r := d.Query(q); names(r) != "02,03,04" || r.Total != 13 {
		t.Errorf("exclude = %s total=%d", names(r), r.Total)
	}
	q.ExcludeID = "not-a-uuid"
	if r := d.Query(q); names(r) != "01,02,03" {
		t.Errorf("invalid exclude = %s", names(r))
	}
	q = render.QuerySpec{Schema: schema, Order: "-updated_at", Limit: 2, Page: 1}
	r := d.Query(q) // updated_at = now() - (20-i)h: post 14 newest
	if names(r) != "14,13" || r.Items[0].UpdatedAt.IsZero() || !r.Items[0].UpdatedAt.After(r.Items[1].UpdatedAt) {
		t.Errorf("-updated_at = %s %+v", names(r), r.Items)
	}
	q.Order = "updated_at"
	if r = d.Query(q); names(r) != "01,02" {
		t.Errorf("updated_at = %s", names(r))
	}

	// Prefetch: one statement fills 12 images; 12 ImageVariants calls add none.
	var urls []string
	for i := 1; i <= 12; i++ {
		urls = append(urls, fmt.Sprintf("/uploads/qd-%s-%02d.jpg", sfx, i))
	}
	before := counter.Count()
	d.PrefetchVariants(urls)
	if n := counter.Count() - before; n > 2 { // the query (and at most a tenant-context statement)
		t.Errorf("prefetch ran %d statements", n)
	}
	before = counter.Count()
	for _, u := range urls {
		got := d.ImageVariants(u)
		if len(got) != 2 || got[0].Width != 600 || got[1].Width != 2000 {
			t.Fatalf("variants of %s = %+v", u, got)
		}
	}
	for _, u := range urls {
		if w, h, ok := d.ImageSize(u); !ok || w != 2000 || h != 1500 {
			t.Fatalf("ImageSize(%s) = %d, %d, %v after a prefetch", u, w, h, ok)
		}
	}
	if n := counter.Count() - before; n != 0 {
		t.Errorf("12 ImageVariants and ImageSize calls after a prefetch ran %d statements, want 0", n)
	}
	if _, _, ok := d.ImageSize("/uploads/not-in-media.jpg"); ok {
		t.Error("ImageSize of an unknown file is ok")
	}
	var _ render.VariantPrefetcher = d
	var _ render.ImageSizer = d

	// ISC-31: a query's statements do not grow with its items. Besides count,
	// list, ancestors, bylines and the prefetch, the card links cost a fixed
	// homepage lookup (navLinks, 4 statements) and the author-schema read.
	run := func(limit int) (items, statements int, sql []string) {
		d := newRenderData(srv)
		before := counter.Count()
		r := d.Query(render.QuerySpec{Schema: schema, Order: "published_at", Limit: limit, Page: 1})
		var imgs []string
		for _, it := range r.Items {
			imgs = append(imgs, it.Image)
			if it.Author != "Quinn" || !strings.HasPrefix(it.URL, base+"/") {
				t.Fatalf("item = %+v", it)
			}
		}
		d.PrefetchVariants(imgs)
		for _, u := range imgs {
			d.ImageVariants(u)
		}
		if d.Failures() != 0 {
			t.Errorf("failures = %d", d.Failures())
		}
		return len(r.Items), counter.Count() - before, counter.Since(before)
	}
	n1, s1, _ := run(1)
	n12, s12, sql := run(12)
	if n1 != 1 || n12 != 12 {
		t.Fatalf("items = %d, %d", n1, n12)
	}
	if s12 != s1 || s12 > 10 {
		t.Errorf("1 item ran %d statements, 12 items %d (want equal, <= 10):\n%s", s1, s12, strings.Join(sql, "\n---\n"))
	}
	if d.Failures() != 0 {
		t.Errorf("failures = %d", d.Failures())
	}
}

// The batch variant lookup must stay on the reverse-URL suffix index: a
// sequential scan of hatamoto_media on every cold loop render grows with the library.
func TestPrefetchVariantsUsesSuffixIndexIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping variants plan test (needs a migrated Postgres)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	sfx := uuid.NewString()[:6]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM hatamoto_media WHERE storage_path LIKE $1`, "pv-"+sfx+"%")
	})
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_media (filename,original_filename,mime_type,file_size,storage_path,public_url,width,variants)
		SELECT 'f'||g, 'f'||g, 'image/jpeg', 1, 'pv-'||$1||g, 'https://cdn.example.test/uploads/pv-'||$1||'-'||g||'.jpg', 2000, '[]'::jsonb
		FROM generate_series(1, 3000) g`, sfx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE hatamoto_media`); err != nil {
		t.Fatal(err)
	}
	var patterns []string
	for i := 1; i <= 12; i++ {
		patterns = append(patterns, likeEscaper.Replace(reverseString(fmt.Sprintf("/uploads/pv-%s-%d.jpg", sfx, i*7)))+"%")
	}
	query, args := variantsQuery(patterns)
	rows, err := pool.Query(ctx, "EXPLAIN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, line)
	}
	text := strings.Join(plan, "\n")
	t.Logf("plan:\n%s", text)
	if strings.Contains(text, "Seq Scan on hatamoto_media") || !strings.Contains(text, "idx_hatamoto_media_public_url_rev") {
		t.Errorf("variants query does not use the suffix index:\n%s", text)
	}
}
