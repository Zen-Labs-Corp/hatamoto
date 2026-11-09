//go:build integration

package media

import (
	"bytes"
	"context"
	"os"
	"testing"
)

// With the bytes in a bucket and the rows in Postgres: upload, folders, usage,
// replace, rebuild and delete leave the local media directory and the temp
// directory empty, and the bucket holds exactly the live files.
func TestS3ServiceAgainstPostgres(t *testing.T) {
	repo := scratchMediaRepo(t)
	spool := t.TempDir()
	t.Setenv("TMPDIR", spool)
	store, fake := newTestS3Store(t, "site/")
	local := t.TempDir()
	svc := NewService(repo, Config{UploadDir: local, BaseURL: "/media", MaxSizeMB: 20, Store: store}, quiet())
	svc.SetFormatPolicy(func(context.Context) Formats { return Formats{WebP: true} })
	bg := context.Background()
	clean := func(step string) {
		t.Helper()
		if left, _ := os.ReadDir(local); len(left) != 0 {
			t.Errorf("%s: local media dir not empty: %v", step, left)
		}
		if left, _ := os.ReadDir(spool); len(left) != 0 {
			t.Errorf("%s: temp files left behind: %v", step, left)
		}
	}
	has := func(key string) bool { _, ok := fake.Objects()["site/"+key]; return ok }

	photo := jpegPhoto(t, 2400, 1600)
	m, err := svc.Upload(bg, nil, bytes.NewReader(photo), "photo.jpg", "", int64(len(photo)))
	if err != nil {
		t.Fatal(err)
	}
	if !has(m.StoragePath) || len(m.Variants) == 0 {
		t.Fatalf("upload: %v / %+v", fake.Objects(), m.Variants)
	}
	for _, v := range m.Variants {
		if !has(v.Path) {
			t.Errorf("variant %s not in the bucket", v.Path)
		}
	}
	clean("upload")

	folder := "Photos"
	if _, err := svc.Update(bg, m.ID, &UpdateMediaInput{Folder: &folder}); err != nil {
		t.Fatal(err)
	}
	if fs, err := svc.Folders(bg); err != nil || len(fs) != 1 || fs[0] != (FolderCount{"/Photos", 1}) {
		t.Fatalf("Folders = %+v %v", fs, err)
	}
	if u, err := svc.Usage(bg, m); err != nil || len(u) != 0 {
		t.Fatalf("Usage = %+v %v", u, err)
	}

	next := jpegPhoto(t, 1200, 800)
	r, err := svc.Replace(bg, m.ID, bytes.NewReader(next), "photo.jpg", int64(len(next)))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Get(bg, m.ID); got.FileSize != int64(len(next)) || *got.Width != 1200 || !has(r.StoragePath) {
		t.Fatalf("replace: %+v", got)
	}
	clean("replace")

	// Drop a variant object, rebuild, and it is back.
	thumb := variantBy(r, "thumbnail", "").Path
	if err := store.Delete(bg, thumb); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegenerateVariants(bg, m.ID); err != nil {
		t.Fatal(err)
	}
	if !has(thumb) {
		t.Error("rebuild did not restore the thumbnail")
	}
	clean("rebuild")

	if err := svc.Delete(bg, m.ID); err != nil {
		t.Fatal(err)
	}
	if len(fake.Objects()) != 0 {
		t.Errorf("objects left after delete: %v", fake.Objects())
	}
	clean("delete")
}
