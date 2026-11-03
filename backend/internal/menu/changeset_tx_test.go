//go:build integration

package menu

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

type csFixture struct {
	db   *database.DB
	svc  *Service
	repo *PostgresRepository
}

func newCSFixture(t *testing.T) *csFixture {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := database.New(context.Background(), config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: 30 * time.Minute, MaxConnIdleTime: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	repo := NewPostgresRepository(db)
	return &csFixture{db: db, svc: NewService(repo, slog.Default()), repo: repo}
}

// apply runs ops in one committed transaction and cleans the menus up afterwards.
func (f *csFixture) apply(t *testing.T, op MenuOp) MenuTxResult {
	t.Helper()
	var res MenuTxResult
	err := f.db.WithTx(context.Background(), func(tx pgx.Tx) error {
		var err error
		res, err = f.svc.ApplyChangesetTx(context.Background(), tx, op)
		return err
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Created {
		t.Cleanup(func() { _ = f.repo.DeleteMenu(context.Background(), res.ID) })
	}
	return res
}

func slugBase(t *testing.T) string { return "cs-" + uuid.NewString()[:8] }

func u(s string) *string { return &s }

func TestChangesetTxCreateWithItems(t *testing.T) {
	f := newCSFixture(t)
	base := slugBase(t)
	res := f.apply(t, MenuOp{Name: "Main " + base, Slug: base, Origin: "plug", Items: []ReplaceItem{
		{Label: "Top", LinkType: LinkTypeCustom, URL: u("/a"), Parent: -1},
		{Label: "Child", LinkType: LinkTypeCustom, URL: u("/b"), Parent: 0},
		{Label: "Plain", LinkType: LinkTypeNone, URL: u("/dropped"), Parent: -1},
	}})
	if !res.Created || res.Slug != base || res.Before != nil {
		t.Fatalf("result = %+v", res)
	}
	m, err := f.repo.GetMenuWithItems(context.Background(), res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Items) != 2 || len(m.Items[0].Children) != 1 || m.Items[0].Children[0].Label != "Child" {
		t.Fatalf("items = %+v", m.Items)
	}
	var origin string
	var loc *string
	conn, _ := f.db.GetConn(context.Background())
	defer conn.Release()
	if err := conn.QueryRow(context.Background(), `SELECT origin, location FROM hatamoto_menus WHERE id=$1`, res.ID).Scan(&origin, &loc); err != nil {
		t.Fatal(err)
	}
	if origin != "plug" || loc != nil {
		t.Fatalf("origin=%q location=%v", origin, loc)
	}
	var url *string
	if err := conn.QueryRow(context.Background(), `SELECT url FROM hatamoto_menu_items WHERE menu_id=$1 AND label='Plain'`, res.ID).Scan(&url); err != nil || url != nil {
		t.Fatalf("plain item url = %v, err %v", url, err)
	}
}

func TestChangesetTxSlugClashGetsSuffix(t *testing.T) {
	f := newCSFixture(t)
	base := slugBase(t)
	a := f.apply(t, MenuOp{Name: "A", Slug: base})
	b := f.apply(t, MenuOp{Name: "B", Slug: base})
	c := f.apply(t, MenuOp{Name: "C", Slug: base})
	if a.Slug != base || b.Slug != base+"-2" || c.Slug != base+"-3" {
		t.Fatalf("slugs %q %q %q", a.Slug, b.Slug, c.Slug)
	}
	// A slug at the column limit still gets a suffix within it.
	long := strings.Repeat("x", maxMenuSlugLen-len(base)) + base
	l1 := f.apply(t, MenuOp{Name: "L1", Slug: long})
	l2 := f.apply(t, MenuOp{Name: "L2", Slug: long})
	if len(l1.Slug) != maxMenuSlugLen || len(l2.Slug) > maxMenuSlugLen || l1.Slug == l2.Slug {
		t.Fatalf("long slugs %q %q", l1.Slug, l2.Slug)
	}
}

func TestChangesetTxConflict(t *testing.T) {
	f := newCSFixture(t)
	ctx := context.Background()
	res := f.apply(t, MenuOp{Name: "Orig", Slug: slugBase(t)})
	m, err := f.repo.GetMenuByID(ctx, res.ID)
	if err != nil {
		t.Fatal(err)
	}
	base := m.UpdatedAt
	time.Sleep(5 * time.Millisecond)
	name := "Moved"
	if _, err := f.repo.UpdateMenu(ctx, res.ID, &UpdateMenuInput{Name: &name}); err != nil {
		t.Fatal(err)
	}

	got := f.apply(t, MenuOp{ID: &res.ID, Name: "Plugin", BaseUpdatedAt: &base})
	if !got.Conflict {
		t.Fatal("moved base token must conflict")
	}
	after, _ := f.repo.GetMenuByID(ctx, res.ID)
	if after.Name != "Moved" {
		t.Fatalf("conflicting op wrote: name = %q", after.Name)
	}
	cur := after.UpdatedAt
	if got := f.apply(t, MenuOp{ID: &res.ID, Name: "Plugin", BaseUpdatedAt: &cur}); got.Conflict {
		t.Fatal("current base token must not conflict")
	}
}

func TestChangesetTxRevertFromPreImage(t *testing.T) {
	f := newCSFixture(t)
	ctx := context.Background()
	res := f.apply(t, MenuOp{Name: "Before", Slug: slugBase(t), Items: []ReplaceItem{
		{Label: "One", LinkType: LinkTypeCustom, URL: u("/1"), Parent: -1},
		{Label: "Two", LinkType: LinkTypeCustom, URL: u("/2"), Parent: 0},
	}})
	orig, err := f.repo.GetMenuWithItems(ctx, res.ID)
	if err != nil {
		t.Fatal(err)
	}

	upd := f.apply(t, MenuOp{ID: &res.ID, Name: "After", Items: []ReplaceItem{{Label: "Other", LinkType: LinkTypeCustom, URL: u("/x"), Parent: -1}}})
	if len(upd.Before) == 0 {
		t.Fatal("update returned no pre-image")
	}
	restore := func(before json.RawMessage) {
		t.Helper()
		if err := f.db.WithTx(ctx, func(tx pgx.Tx) error { return f.svc.RestoreChangesetTx(ctx, tx, before) }); err != nil {
			t.Fatal(err)
		}
	}
	same := func() {
		t.Helper()
		got, err := f.repo.GetMenuWithItems(ctx, res.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != orig.Name || got.Slug != orig.Slug || len(got.Items) != 1 || got.Items[0].ID != orig.Items[0].ID ||
			len(got.Items[0].Children) != 1 || got.Items[0].Children[0].ID != orig.Items[0].Children[0].ID {
			t.Fatalf("restored menu differs:\n got %+v\nwant %+v", got, orig)
		}
	}
	restore(upd.Before)
	same()

	// A deleted menu comes back with the same id and items.
	del := f.apply(t, MenuOp{ID: &res.ID, Delete: true})
	if _, err := f.repo.GetMenuByID(ctx, res.ID); !errors.Is(err, ErrMenuNotFound) {
		t.Fatalf("menu still there after delete: %v", err)
	}
	restore(del.Before)
	same()
}

func TestChangesetTxLocationRefused(t *testing.T) {
	f := newCSFixture(t)
	err := f.db.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, err := f.svc.ApplyChangesetTx(context.Background(), tx, MenuOp{Name: "Loc", Slug: slugBase(t), Location: "header"})
		return err
	})
	if !errors.Is(err, ErrLocationNotAllowed) {
		t.Fatalf("err = %v, want ErrLocationNotAllowed", err)
	}
}

func TestChangesetTxRollbackLeavesNothing(t *testing.T) {
	f := newCSFixture(t)
	ctx := context.Background()
	slug := slugBase(t)
	boom := errors.New("boom")
	err := f.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := f.svc.ApplyChangesetTx(ctx, tx, MenuOp{Name: "Gone", Slug: slug}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.repo.GetMenuBySlug(ctx, slug); !errors.Is(err, ErrMenuNotFound) {
		t.Fatalf("rolled-back menu visible: %v", err)
	}
}

func TestChangesetTxValidationMatchesReplaceMenu(t *testing.T) {
	f := newCSFixture(t)
	bad := `a"b`
	cases := map[string][]ReplaceItem{
		"link type": {{Label: "x", LinkType: "bogus", Parent: -1}},
		"classes":   {{Label: "x", LinkType: LinkTypeCustom, CSSClasses: &bad, Parent: -1}},
		"attrs":     {{Label: "x", LinkType: LinkTypeCustom, Attrs: MenuItemAttrs{Rel: []string{"x"}}, Parent: -1}},
	}
	seed := f.apply(t, MenuOp{Name: "Seed", Slug: slugBase(t)})
	for name, items := range cases {
		_, wantErr := f.svc.ReplaceMenu(context.Background(), seed.ID, nil, items)
		gotErr := f.db.WithTx(context.Background(), func(tx pgx.Tx) error {
			_, err := f.svc.ApplyChangesetTx(context.Background(), tx, MenuOp{ID: &seed.ID, Name: "Seed", Items: items})
			return err
		})
		if gotErr == nil {
			t.Errorf("%s: ApplyChangesetTx accepted invalid items", name)
		}
		if wantErr == nil || gotErr.Error() != wantErr.Error() {
			t.Errorf("%s: got %v, ReplaceMenu gave %v", name, gotErr, wantErr)
		}
	}
}

func TestChangesetTxRejectsBadParentIndex(t *testing.T) {
	f := newCSFixture(t)
	err := f.db.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, err := f.svc.ApplyChangesetTx(context.Background(), tx, MenuOp{Name: "P", Slug: slugBase(t),
			Items: []ReplaceItem{{Label: "x", LinkType: LinkTypeCustom, Parent: 3}}})
		return err
	})
	if !errors.Is(err, ErrInvalidMenu) {
		t.Fatalf("err = %v, want ErrInvalidMenu", err)
	}
}
