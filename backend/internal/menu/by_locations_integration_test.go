//go:build integration

package menu

import (
	"context"
	"io"
	"log/slog"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/testutil"
)

func TestGetMenusWithItemsByLocations(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pcfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	var counter testutil.QueryCounter
	pcfg.ConnConfig.Tracer = &counter
	pcfg.MaxConns, pcfg.MinConns = 4, 1
	db, err := database.NewWithConfig(ctx, pcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	repo := NewPostgresRepository(db)

	sfx := uuid.NewString()[:8]
	locA, locB, locNone := "it-a-"+sfx, "it-b-"+sfx, "it-none-"+sfx
	str := func(s string) *string { return &s }
	mk := func(name, loc string) *Menu {
		t.Helper()
		m, err := repo.CreateMenu(ctx, &CreateMenuInput{Name: name, Slug: name + "-" + sfx, Location: &loc})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.DeleteMenu(ctx, m.ID) })
		return m
	}
	item := func(m *Menu, label string, parent *uuid.UUID, pos int) *MenuItem {
		t.Helper()
		it, err := repo.CreateMenuItem(ctx, m.ID, &CreateMenuItemInput{
			Label: label, LinkType: "external", URL: str("https://example.test/" + label), ParentID: parent, Position: &pos,
		})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}

	oldA := mk("old-a", locA)
	time.Sleep(10 * time.Millisecond)
	newA := mk("new-a", locA) // same location, created later: must lose
	b := mk("b", locB)
	parent := item(oldA, "parent", nil, 0)
	item(oldA, "child-2", &parent.ID, 2)
	item(oldA, "child-1", &parent.ID, 1)
	item(oldA, "second", nil, 1)
	item(newA, "ignored", nil, 0)
	item(b, "only", nil, 0)

	want := func(m *Menu) *Menu {
		t.Helper()
		got, err := repo.GetMenuWithItems(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	t.Run("none", func(t *testing.T) {
		before := counter.Count()
		got, err := repo.GetMenusWithItemsByLocations(ctx, nil)
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
		if n := counter.Count() - before; n != 0 {
			t.Errorf("%d statements for no locations, want 0", n)
		}
	})

	t.Run("two locations and a missing one", func(t *testing.T) {
		before := counter.Count()
		got, err := repo.GetMenusWithItemsByLocations(ctx, []string{locA, locB, locNone})
		if err != nil {
			t.Fatal(err)
		}
		if n := counter.Count() - before; n != 2 {
			t.Errorf("%d statements, want 2", n)
		}
		if len(got) != 2 || got[locNone] != nil {
			t.Fatalf("got %d menus (%v), want the 2 existing", len(got), got[locNone])
		}
		if !reflect.DeepEqual(got[locA], want(oldA)) {
			t.Errorf("%s: %+v\nwant (the oldest menu with its tree) %+v", locA, got[locA], want(oldA))
		}
		if !reflect.DeepEqual(got[locB], want(b)) {
			t.Errorf("%s: %+v\nwant %+v", locB, got[locB], want(b))
		}
		if len(got[locA].Items) != 2 || got[locA].Items[0].Label != "parent" || len(got[locA].Items[0].Children) != 2 ||
			got[locA].Items[0].Children[0].Label != "child-1" {
			t.Errorf("tree/order wrong: %+v", got[locA].Items)
		}
	})

	t.Run("one location equals GetMenuByLocation", func(t *testing.T) {
		got, err := repo.GetMenusWithItemsByLocations(ctx, []string{locB})
		if err != nil {
			t.Fatal(err)
		}
		single, err := repo.GetMenuByLocation(ctx, locB)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got[locB], want(single)) {
			t.Errorf("got %+v, want %+v", got[locB], want(single))
		}
	})

	t.Run("no menu at all", func(t *testing.T) {
		before := counter.Count()
		got, err := repo.GetMenusWithItemsByLocations(ctx, []string{locNone})
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
		if n := counter.Count() - before; n != 1 {
			t.Errorf("%d statements, want 1", n)
		}
	})

	t.Run("service drops invalid locations", func(t *testing.T) {
		svc := NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
		got, err := svc.GetMenusWithItemsByLocations(ctx, []string{"Not Valid!", locB})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[locB] == nil {
			t.Errorf("got %v, want only %s", got, locB)
		}
	})
}
