//go:build integration

package menu

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

func seedTestService(t *testing.T) (*Service, *PostgresRepository, func(loc string) int) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 16, MinConnections: 1,
		MaxConnLifetime: 30 * time.Minute, MaxConnIdleTime: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	repo := NewPostgresRepository(db)
	count := func(loc string) int {
		conn, err := db.GetConn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		var n int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM hatamoto_menus WHERE location = $1`, loc).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	return NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil))), repo, count
}

func uniqueLoc() string { return "t" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] }

func cleanupLocation(t *testing.T, repo *PostgresRepository, loc string) {
	t.Cleanup(func() {
		ctx := context.Background()
		ms, _ := repo.ListMenus(ctx)
		for _, m := range ms {
			if strings.HasPrefix(m.Slug, loc) || (m.Location != nil && *m.Location == loc) {
				_ = repo.DeleteMenu(ctx, m.ID)
			}
		}
	})
}

func TestSeedDeclCreatesOnceAndNeverOverwrites(t *testing.T) {
	svc, repo, count := seedTestService(t)
	ctx := context.Background()
	loc := uniqueLoc()
	cleanupLocation(t, repo, loc)
	decl := []DeclLocation{{Name: loc, Title: "Follow", Default: []DeclItem{
		{Label: "Blog", Archive: "post"}, {Label: "Contact", URL: "/contact"}}}}

	created, err := svc.SeedDecl(ctx, decl)
	if err != nil || len(created) != 1 || created[0] != loc {
		t.Fatalf("first seed = %v, %v", created, err)
	}
	m, err := repo.GetMenuByLocation(ctx, loc)
	if err != nil || m.Slug != loc || m.Name != "Follow" {
		t.Fatalf("menu = %+v, %v", m, err)
	}
	full, err := repo.GetMenuWithItems(ctx, m.ID)
	if err != nil || len(full.Items) != 2 || full.Items[0].LinkType != LinkTypeArchive ||
		full.Items[0].SchemaSlug == nil || *full.Items[0].SchemaSlug != "post" ||
		full.Items[1].URL == nil || *full.Items[1].URL != "/contact" {
		t.Fatalf("items = %+v, %v", full, err)
	}

	// The owner edits the menu; seeding again must not touch it.
	name := "Mine"
	if err := repo.ReplaceMenu(ctx, m.ID, &UpdateMenuInput{Name: &name}, []ReplaceItem{{Label: "Only", LinkType: LinkTypeCustom, URL: &name, Parent: -1}}); err != nil {
		t.Fatal(err)
	}
	if created, err = svc.SeedDecl(ctx, decl); err != nil || len(created) != 0 {
		t.Fatalf("second seed = %v, %v", created, err)
	}
	full, _ = repo.GetMenuWithItems(ctx, m.ID)
	if full.Name != "Mine" || len(full.Items) != 1 {
		t.Fatalf("existing menu modified: %+v", full)
	}
	if count(loc) != 1 {
		t.Fatalf("menus at location = %d", count(loc))
	}
}

func TestSeedDeclConcurrentCreatesOneMenu(t *testing.T) {
	svc, repo, count := seedTestService(t)
	loc := uniqueLoc()
	cleanupLocation(t, repo, loc)
	decl := []DeclLocation{{Name: loc, Title: "Follow", Default: []DeclItem{{Label: "A", URL: "/a"}}}}

	var wg sync.WaitGroup
	var mu sync.Mutex
	made := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := svc.SeedDecl(context.Background(), decl)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			made += len(c)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if n := count(loc); n != 1 || made != 1 {
		t.Fatalf("menus = %d, creations reported = %d, want 1 and 1", n, made)
	}
}

func TestEnsureMenuAtLocationSlugCollision(t *testing.T) {
	svc, repo, _ := seedTestService(t)
	ctx := context.Background()
	loc := uniqueLoc()
	cleanupLocation(t, repo, loc)
	other, err := repo.CreateMenu(ctx, &CreateMenuInput{Name: "Taken", Slug: loc})
	if err != nil {
		t.Fatal(err)
	}
	m, created, err := svc.EnsureMenuAtLocation(ctx, EnsureInput{Name: "X", Slug: loc, Location: loc})
	if err != nil || !created || m.Slug != loc+"-menu" || m.ID == other.ID {
		t.Fatalf("ensure = %+v, %v, %v", m, created, err)
	}
}

func TestTakeLocationUnassignsPreviousHolder(t *testing.T) {
	_, repo, count := seedTestService(t)
	ctx := context.Background()
	loc := uniqueLoc()
	cleanupLocation(t, repo, loc)
	a, err := repo.CreateMenu(ctx, &CreateMenuInput{Name: "A", Slug: loc + "-a", Location: &loc, TakeLocation: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := repo.CreateMenu(ctx, &CreateMenuInput{Name: "B", Slug: loc + "-b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateMenu(ctx, b.ID, &UpdateMenuInput{Location: &loc, TakeLocation: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.GetMenuByLocation(ctx, loc); got == nil || got.ID != b.ID || count(loc) != 1 {
		t.Fatalf("holder after UpdateMenu = %+v, count %d", got, count(loc))
	}
	if got, err := repo.GetMenuByID(ctx, a.ID); err != nil || got.Location != nil {
		t.Fatalf("previous holder = %+v, %v; want kept, unassigned", got, err)
	}

	// ReplaceMenu and CreateMenu take it as well.
	if err := repo.ReplaceMenu(ctx, a.ID, &UpdateMenuInput{Location: &loc, TakeLocation: true}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.GetMenuByLocation(ctx, loc); got.ID != a.ID || count(loc) != 1 {
		t.Fatalf("holder after ReplaceMenu = %+v, count %d", got, count(loc))
	}
	c, err := repo.CreateMenu(ctx, &CreateMenuInput{Name: "C", Slug: loc + "-c", Location: &loc, TakeLocation: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.GetMenuByLocation(ctx, loc); got.ID != c.ID || count(loc) != 1 {
		t.Fatalf("holder after CreateMenu = %+v, count %d", got, count(loc))
	}
}

// Without TakeLocation (plugin API, REST) holders coexist as before.
func TestSaveWithoutTakeLocationLeavesHolders(t *testing.T) {
	_, repo, count := seedTestService(t)
	ctx := context.Background()
	loc := uniqueLoc()
	cleanupLocation(t, repo, loc)
	a, err := repo.CreateMenu(ctx, &CreateMenuInput{Name: "A", Slug: loc + "-a", Location: &loc})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateMenu(ctx, &CreateMenuInput{Name: "B", Slug: loc + "-b", Location: &loc}); err != nil {
		t.Fatal(err)
	}
	c, err := repo.CreateMenu(ctx, &CreateMenuInput{Name: "C", Slug: loc + "-c"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateMenu(ctx, c.ID, &UpdateMenuInput{Location: &loc}); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceMenu(ctx, a.ID, &UpdateMenuInput{Location: &loc}, nil); err != nil {
		t.Fatal(err)
	}
	if n := count(loc); n != 3 {
		t.Fatalf("holders = %d, want 3 (no take-over)", n)
	}
}

// Re-saving a holder with its current location never unassigns a legacy duplicate.
func TestTakeLocationOnlyWhenLocationChanges(t *testing.T) {
	_, repo, count := seedTestService(t)
	ctx := context.Background()
	loc := uniqueLoc()
	cleanupLocation(t, repo, loc)
	older, err := repo.CreateMenu(ctx, &CreateMenuInput{Name: "Old", Slug: loc + "-old", Location: &loc})
	if err != nil {
		t.Fatal(err)
	}
	newer, err := repo.CreateMenu(ctx, &CreateMenuInput{Name: "New", Slug: loc + "-new", Location: &loc})
	if err != nil {
		t.Fatal(err)
	}
	name := "New renamed"
	if err := repo.ReplaceMenu(ctx, newer.ID, &UpdateMenuInput{Name: &name, Location: &loc, TakeLocation: true}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateMenu(ctx, newer.ID, &UpdateMenuInput{Name: &name, Location: &loc, TakeLocation: true}); err != nil {
		t.Fatal(err)
	}
	if n := count(loc); n != 2 {
		t.Fatalf("holders = %d, want 2 after re-saves", n)
	}
	if got, _ := repo.GetMenuByLocation(ctx, loc); got == nil || got.ID != older.ID {
		t.Fatalf("rendered holder = %+v, want the older menu", got)
	}
}
