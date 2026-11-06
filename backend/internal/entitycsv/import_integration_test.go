//go:build integration

package entitycsv

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
)

// A dry run leaves the table alone, the import creates drafts, a second
// import updates them, and a mapped Status column publishes.
func TestRunAgainstDatabase(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: 30 * time.Minute, MaxConnIdleTime: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	schemas := entity.NewPostgresSchemaRepository(db)
	repo := entity.NewPostgresEntityRepository(db)
	svc := entity.NewService(schemas, repo, slog.Default())

	slug := "csv-" + uuid.NewString()[:8]
	sc, err := schemas.Create(ctx, &entity.CreateSchemaInput{Name: slug, Slug: slug, Schema: jsontext.Value(strings.Replace(importSchema, `"required":["title"],`, "", 1))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = schemas.Delete(ctx, sc.ID) })
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, sc.ID)
	})
	count := func() int {
		var n int
		if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities WHERE schema_id = $1`, sc.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	const n = 12
	build := func(extra string) string {
		var sb strings.Builder
		sb.WriteString("sku,title,price,tags,up" + extra + "\n")
		for i := range n {
			up := ""
			if i > 0 {
				up = "0"
			}
			status := ""
			if extra != "" {
				status = ",published"
			}
			fmt.Fprintf(&sb, "%d,Item %d,%d.5,a|b,%s%s\n", i, i, i, up, status)
		}
		return sb.String()
	}
	m := Mapping{Type: slug, Cols: map[string]string{"sku": "key", "title": "name", "price": "attr:price", "tags": "attr:tags", "up": "parent_key"}}
	run := func(csv string, m Mapping, dry bool) *Report {
		t.Helper()
		rep, err := Run(ctx, svc, nil, sc, m, strings.NewReader(csv), dry)
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}

	rep := run(build(""), m, true)
	if rep.Created != n || rep.Failed != 0 || count() != 0 {
		t.Fatalf("dry run = %+v, rows %d", rep, count())
	}
	rep = run(build(""), m, false)
	if rep.Created != n || rep.Failed != 0 || count() != n {
		t.Fatalf("import = %+v, rows %d", rep, count())
	}
	var drafts int
	_ = db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities WHERE schema_id = $1 AND status = 'draft'`, sc.ID).Scan(&drafts)
	if drafts != n {
		t.Fatalf("drafts = %d, want %d", drafts, n)
	}
	e, err := repo.GetByImportKey(ctx, "csv:"+slug+":3")
	if err != nil || e.ParentID == nil || KeyOf(e.Metadata, slug) != "3" {
		t.Fatalf("row 3 = %+v (%v)", e, err)
	}

	rep = run(build(""), m, false)
	if rep.Created != 0 || rep.Updated != n || count() != n {
		t.Fatalf("second import = %+v, rows %d", rep, count())
	}

	m.Cols["state"] = "status"
	rep = run(build(",state"), m, false)
	if rep.Updated != n || rep.Failed != 0 {
		t.Fatalf("publish import = %+v", rep)
	}
	var published int
	_ = db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities WHERE schema_id = $1 AND status = 'published'`, sc.ID).Scan(&published)
	if published != n {
		t.Fatalf("published = %d, want %d", published, n)
	}
}

// A re-import by key changes only the mapped columns: other attributes,
// status, published date, created date and parent stay, an empty cell keeps
// the stored value, and the default status is for new rows only.
func TestReimportKeepsWhatTheFileDoesNotCarry(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: 30 * time.Minute, MaxConnIdleTime: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	schemas := entity.NewPostgresSchemaRepository(db)
	repo := entity.NewPostgresEntityRepository(db)
	svc := entity.NewService(schemas, repo, slog.Default())
	slug := "csvre-" + uuid.NewString()[:8]
	sc, err := schemas.Create(ctx, &entity.CreateSchemaInput{Name: slug, Slug: slug, Schema: jsontext.Value(strings.Replace(importSchema, `"required":["title"],`, "", 1))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = schemas.Delete(ctx, sc.ID) })
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, sc.ID)
	})
	run := func(csv string, m Mapping) *Report {
		t.Helper()
		rep, err := Run(ctx, svc, nil, sc, m, strings.NewReader(csv), false)
		if err != nil || rep.Failed != 0 {
			t.Fatalf("run = %+v, %v", rep, err)
		}
		return rep
	}
	full := Mapping{Type: slug, Cols: map[string]string{"sku": "key", "title": "name", "price": "attr:price", "stock": "attr:stock", "up": "parent_key", "state": "status", "pub": "published_at"}}
	run("sku,title,price,stock,up,state,pub\nroot,Root,1,1,,published,2026-01-02\nkid,Kid,2,7,root,published,2026-01-03\n", full)
	before, err := repo.GetByImportKey(ctx, "csv:"+slug+":kid")
	if err != nil || before.ParentID == nil || before.Status != entity.StatusPublished || before.PublishedAt == nil {
		t.Fatalf("setup row = %+v (%v)", before, err)
	}

	part := Mapping{Type: slug, Status: entity.StatusDraft, Cols: map[string]string{"sku": "key", "title": "name", "price": "attr:price"}}
	rep := run("sku,title,price\nkid,Kid renamed,9\nnew,New,\n", part)
	if rep.Updated != 1 || rep.Created != 1 {
		t.Fatalf("report = %+v", rep)
	}
	after, err := repo.GetByImportKey(ctx, "csv:"+slug+":kid")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(strings.Fields(string(after.Attributes)), "") != `{"price":9,"stock":7}` {
		t.Errorf("attributes = %s, want price changed and stock kept", after.Attributes)
	}
	if after.Name != "Kid renamed" || after.Status != entity.StatusPublished || after.PublishedAt == nil || !after.PublishedAt.Equal(*before.PublishedAt) ||
		!after.CreatedAt.Equal(before.CreatedAt) || after.ParentID == nil || *after.ParentID != *before.ParentID || after.Slug != before.Slug {
		t.Errorf("row after = %+v, want status, dates, parent and slug kept (before %+v)", after, before)
	}
	fresh, err := repo.GetByImportKey(ctx, "csv:"+slug+":new")
	if err != nil || fresh.Status != entity.StatusDraft {
		t.Errorf("new row = %+v (%v), want a draft", fresh, err)
	}
}

// A JSON object cell replaces the stored object (a member it lacks is gone);
// flattened columns of an object merge within the object's mapped keys only.
func TestReimportJSONCellReplacesObject(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: 30 * time.Minute, MaxConnIdleTime: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	schemas := entity.NewPostgresSchemaRepository(db)
	repo := entity.NewPostgresEntityRepository(db)
	svc := entity.NewService(schemas, repo, slog.Default())
	slug := "csvjs-" + uuid.NewString()[:8]
	sc, err := schemas.Create(ctx, &entity.CreateSchemaInput{Name: slug, Slug: slug, Schema: jsontext.Value(
		`{"type":"object","properties":{"seo":{"type":"object"},"meta":{"type":"object","properties":{"title":{"type":"string"},"desc":{"type":"string"}}}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = schemas.Delete(ctx, sc.ID) })
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, sc.ID)
	})
	m := Mapping{Type: slug, Cols: map[string]string{"sku": "key", "name": "name", "seo": "attr:seo", "t": "attr:meta.title"}}
	run := func(csv string) {
		t.Helper()
		rep, err := Run(ctx, svc, nil, sc, m, strings.NewReader(csv), false)
		if err != nil || rep.Failed != 0 {
			t.Fatalf("run = %+v, %v", rep, err)
		}
	}
	run("sku,name,seo,t\na,A,\"{\"\"title\"\":\"\"S\"\",\"\"noindex\"\":true}\",T\n")
	if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_entities SET attributes = jsonb_set(attributes, '{meta,desc}', '"D"') WHERE schema_id = $1`, sc.ID); err != nil {
		t.Fatal(err)
	}
	run("sku,name,seo,t\na,A,\"{\"\"title\"\":\"\"S\"\"}\",T2\n")
	got, err := repo.GetByImportKey(ctx, "csv:"+slug+":a")
	if err != nil {
		t.Fatal(err)
	}
	var attrs map[string]map[string]any
	if err := json.Unmarshal(got.Attributes, &attrs); err != nil {
		t.Fatal(err)
	}
	if len(attrs["seo"]) != 1 || attrs["seo"]["title"] != "S" || len(attrs["meta"]) != 2 || attrs["meta"]["desc"] != "D" || attrs["meta"]["title"] != "T2" {
		t.Errorf("attributes = %s, want seo replaced (noindex gone) and meta.desc kept", got.Attributes)
	}
}
