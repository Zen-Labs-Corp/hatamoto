//go:build integration

package entitycsv

import (
	"context"
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
