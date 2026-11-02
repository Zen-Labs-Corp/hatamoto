//go:build integration

package packages

import (
	"context"
	"errors"
	"sync"
	"testing"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/menu"
)

const (
	followDecl = `"follow":{"title":"Follow","default":[{"label":"Blog","archive":"post"},{"label":"About","url":"/about"}]}`
	legalDecl  = `"legal":{"title":"Legal","default":[{"label":"Terms","url":"/terms"}]}`
)

func (r *rig) menuSvc() *menu.Service {
	r.t.Helper()
	pc := r.pool.Config()
	db, err := database.NewWithConfig(context.Background(), pc)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(db.Close)
	svc := menu.NewService(menu.NewPostgresRepository(db), r.mgr.Logger)
	r.mgr.MenuSeeder = svc
	return svc
}

func (r *rig) menuTheme(version string, decls ...string) map[string][]byte {
	menus := ""
	for i, d := range decls {
		if i > 0 {
			menus += ","
		}
		menus += d
	}
	files := r.theme(version)
	files[ThemeManifest] = []byte(`{"name":"sunrise","version":"` + version + `","menus":{` + menus + `}}`)
	return files
}

func (r *rig) installTheme(files map[string][]byte) {
	r.t.Helper()
	raw, err := PackFiles(files, &r.key)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.mgr.Install(context.Background(), raw, InstallRequest{Source: SourceUpload}); err != nil {
		r.t.Fatal(err)
	}
}

func menusAt(t *testing.T, svc *menu.Service, loc string) *menu.Menu {
	t.Helper()
	m, err := svc.GetMenuByLocation(context.Background(), loc)
	if errors.Is(err, menu.ErrMenuNotFound) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	full, err := svc.GetMenuWithItems(context.Background(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return full
}

func countMenus(t *testing.T, r *rig) int {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM hatamoto_menus`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestActivateSeedsDeclaredMenus(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	svc := r.menuSvc()
	r.installTheme(r.menuTheme("1.0.0", followDecl))
	if countMenus(t, r) != 0 {
		t.Fatal("install must not seed")
	}
	if err := r.mgr.Activate(ctx, "sunrise", true); err != nil {
		t.Fatal(err)
	}
	m := menusAt(t, svc, "follow")
	if m == nil || len(m.Items) != 2 || m.Items[0].Label != "Blog" {
		t.Fatalf("follow menu = %+v", m)
	}

	// Concurrent and repeated activation keep one menu.
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = r.mgr.Activate(ctx, "sunrise", true) }()
	}
	wg.Wait()
	if n := countMenus(t, r); n != 1 {
		t.Fatalf("menus = %d, want 1", n)
	}

	// Owner edits are never overwritten.
	if _, err := svc.ReplaceMenu(ctx, m.ID, nil, []menu.ReplaceItem{{Label: "Mine", LinkType: menu.LinkTypeExternal, URL: ptr("/mine"), Parent: -1}}); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.Activate(ctx, "sunrise", true); err != nil {
		t.Fatal(err)
	}
	if got := menusAt(t, svc, "follow"); len(got.Items) != 1 || got.Items[0].Label != "Mine" {
		t.Fatalf("owner edit lost: %+v", got.Items)
	}

	// A deleted menu stays deleted on restart-like reconcile, and returns on re-activation.
	if err := svc.DeleteMenu(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	r.mgr.Reconcile(ctx)
	if menusAt(t, svc, "follow") != nil {
		t.Fatal("reconcile re-created a deleted menu")
	}
	if err := r.mgr.Activate(ctx, "sunrise", true); err != nil {
		t.Fatal(err)
	}
	if menusAt(t, svc, "follow") == nil {
		t.Fatal("re-activation did not seed")
	}
}

func TestActivateSeedsNothingWhenTheThemeFailedToLoad(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	svc := r.menuSvc()
	r.installTheme(r.menuTheme("1.0.0", followDecl))
	if err := r.mgr.Activate(ctx, "sunrise", true); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteMenu(ctx, menusAt(t, svc, "follow").ID); err != nil {
		t.Fatal(err)
	}
	r.mgr.theme.err = errors.New("theme not applied")
	if err := r.mgr.Activate(ctx, "sunrise", true); err == nil {
		t.Fatal("activation must report the load failure")
	}
	if menusAt(t, svc, "follow") != nil {
		t.Fatal("a theme that failed to load seeded its menus")
	}
}

func TestUpdateSeedsOnlyNewLocationsOfActiveTheme(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	svc := r.menuSvc()
	r.installTheme(r.menuTheme("1.0.0", followDecl))
	if err := r.mgr.Activate(ctx, "sunrise", true); err != nil {
		t.Fatal(err)
	}
	if m := menusAt(t, svc, "follow"); m == nil {
		t.Fatal("follow not seeded")
	}
	// The owner deleted follow; an update that keeps declaring it must not bring it back.
	if err := svc.DeleteMenu(ctx, menusAt(t, svc, "follow").ID); err != nil {
		t.Fatal(err)
	}
	r.installTheme(r.menuTheme("1.1.0", followDecl))
	if countMenus(t, r) != 0 {
		t.Fatal("update without a new location seeded a menu")
	}
	r.installTheme(r.menuTheme("1.2.0", followDecl, legalDecl))
	if menusAt(t, svc, "legal") == nil || menusAt(t, svc, "follow") != nil {
		t.Fatal("update must seed only the new location")
	}
}

func TestUpdateOfInactiveThemeSeedsNothing(t *testing.T) {
	r := newRig(t)
	r.menuSvc()
	r.installTheme(r.menuTheme("1.0.0", followDecl))
	r.installTheme(r.menuTheme("1.1.0", followDecl, legalDecl))
	if n := countMenus(t, r); n != 0 {
		t.Fatalf("menus = %d, want 0", n)
	}
}

func ptr(s string) *string { return &s }
