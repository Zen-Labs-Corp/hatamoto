//go:build integration

package media

import (
	"context"
	"testing"
)

// Adopt writes the rows the operator would otherwise write by hand, and a
// second run finds nothing left.
func TestAdoptIntegration(t *testing.T) {
	repo := scratchMediaRepo(t)
	st, err := NewLocalStore(t.TempDir(), "/media")
	if err != nil {
		t.Fatal(err)
	}
	putKeys(t, st, "wp/2020/01/hero.png", "logo.png")
	l := st.(Lister)
	ctx := context.Background()

	dry, err := Adopt(ctx, repo, l, AdoptOptions{Dir: "/", Provider: "local"})
	if err != nil || len(dry.Items) != 2 || dry.Adopted != 0 {
		t.Fatalf("dry run = %+v %v", dry, err)
	}
	if keys, _ := repo.ReferencedKeys(ctx); len(keys) != 0 {
		t.Fatalf("dry run created rows: %v", keys)
	}

	res, err := Adopt(ctx, repo, l, AdoptOptions{Dir: "/", Apply: true, Provider: "local"})
	if err != nil || res.Adopted != 2 {
		t.Fatalf("apply = %+v %v", res, err)
	}
	var filename, orig, mime, provider, path, url, folder, origin, variants string
	var size int64
	conn, err := repo.db.GetConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	err = conn.QueryRow(ctx, `SELECT filename, original_filename, mime_type, file_size, storage_provider,
		storage_path, public_url, folder, origin, variants::text FROM hatamoto_media WHERE storage_path = 'wp/2020/01/hero.png'`).
		Scan(&filename, &orig, &mime, &size, &provider, &path, &url, &folder, &origin, &variants)
	if err != nil {
		t.Fatal(err)
	}
	if filename != "hero.png" || orig != "hero.png" || mime != "image/png" || size != 1 || provider != "local" ||
		path != "wp/2020/01/hero.png" || url != "/media/wp/2020/01/hero.png" || folder != "/wp/2020/01" || origin != "owner" || variants != "[]" {
		t.Errorf("row = %s %s %s %d %s %s %s %s %s %s", filename, orig, mime, size, provider, path, url, folder, origin, variants)
	}

	again, err := Adopt(ctx, repo, l, AdoptOptions{Dir: "/", Apply: true, Provider: "local"})
	if err != nil || again.Adopted != 0 || again.AlreadyHaveRow != 2 {
		t.Errorf("second run = %+v %v, want 0 adopted and 2 skipped", again, err)
	}
}
