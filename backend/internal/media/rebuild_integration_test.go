//go:build integration

package media

import (
	"bytes"
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/migrations"
)

// scratchMediaRepo migrates a fresh database next to DATABASE_URL.
func scratchMediaRepo(t *testing.T) *PostgresRepository {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_media_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pcfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.NewWithConfig(ctx, pcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	var sdb *sql.DB = stdlib.OpenDBFromPool(db.Pool())
	defer sdb.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, sdb, migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewPostgresRepository(db)
}

// A rebuild that is cut off resumes from its stored cursor on a new server and
// ends at N of N, with every image holding modern copies and none redone.
func TestRebuildResumesAfterRestart(t *testing.T) {
	const n = 6
	repo := scratchMediaRepo(t)
	svc := NewService(repo, Config{UploadDir: t.TempDir(), BaseURL: "/media", MaxSizeMB: 20}, quiet())
	svc.SetFormatPolicy(func(context.Context) Formats { return Formats{} }) // uploads without modern copies
	bg := context.Background()
	for range n {
		if _, err := svc.Upload(bg, nil, bytes.NewReader(jpegPhoto(t, 1600, 1000)), "p.jpg", "", 0); err != nil {
			t.Fatal(err)
		}
	}
	if c, err := repo.CountImages(bg); err != nil || c != n {
		t.Fatalf("count %d %v", c, err)
	}
	svc.SetFormatPolicy(func(context.Context) Formats { return Formats{WebP: true} })

	store := &memState{}
	counting := &countingSource{RebuildSource: svc, calls: map[uuid.UUID]int{}}
	ctx1, cancel1 := context.WithCancel(bg)
	counting.after = func(k int) {
		if k == 3 {
			cancel1()
		}
	}
	r1 := NewRebuilder(counting, store, func() context.Context { return ctx1 }, RebuildHooks{}, quiet())
	if err := r1.Start(bg, "owner"); err != nil {
		t.Fatal(err)
	}
	r1.Wait(bg)
	if st, _ := r1.Status(bg); !st.Running || st.Done >= n {
		t.Fatalf("expected an interrupted run: %+v", st)
	}

	counting.after = nil
	r2 := NewRebuilder(counting, store, func() context.Context { return bg }, RebuildHooks{}, quiet())
	r2.Resume(bg)
	r2.Wait(bg)
	st, _ := r2.Status(bg)
	if st.Running || st.Done != n || st.Total != n {
		t.Fatalf("after resume: %+v", st)
	}
	redone := 0
	for _, c := range counting.calls {
		if c > 1 {
			redone++
		}
	}
	if len(counting.calls) != n || redone > 1 {
		t.Errorf("%d images seen, %d redone", len(counting.calls), redone)
	}
	all, err := repo.ListImagesAfter(bg, uuid.Nil, 100)
	if err != nil || len(all) != n {
		t.Fatalf("list %d %v", len(all), err)
	}
	for _, m := range all {
		if variantBy(m, "large", "image/webp") == nil {
			t.Errorf("%s has no webp copy: %+v", m.ID, m.Variants)
		}
	}
}

type countingSource struct {
	RebuildSource
	calls map[uuid.UUID]int
	after func(k int)
}

func (c *countingSource) RegenerateVariants(ctx context.Context, id uuid.UUID) (*Media, error) {
	m, err := c.RebuildSource.RegenerateVariants(ctx, id)
	c.calls[id]++
	if c.after != nil {
		c.after(len(c.calls))
	}
	return m, err
}

// ListAllAfter walks every record, whatever its type, by id.
func TestListAllAfterWalksEveryRecordOnce(t *testing.T) {
	repo := scratchMediaRepo(t)
	ctx := context.Background()
	want := map[uuid.UUID]bool{}
	for i, mt := range []string{"image/png", "application/pdf", "video/mp4", "image/jpeg", "application/pdf"} {
		m := &Media{ID: uuid.New(), Filename: "f" + string(rune('a'+i)), OriginalFilename: "o", MimeType: mt,
			StorageProvider: "local", StoragePath: "p/" + string(rune('a'+i)), Variants: []ImageVariant{}}
		if err := repo.Create(ctx, m); err != nil {
			t.Fatal(err)
		}
		want[m.ID] = true
	}
	got := map[uuid.UUID]bool{}
	var after uuid.UUID
	for {
		page, err := repo.ListAllAfter(ctx, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range page {
			if got[m.ID] {
				t.Errorf("%s seen twice", m.ID)
			}
			got[m.ID] = true
		}
		if len(page) < 2 {
			break
		}
		after = page[len(page)-1].ID
	}
	if len(got) != len(want) {
		t.Errorf("walked %d records, want %d", len(got), len(want))
	}
}
