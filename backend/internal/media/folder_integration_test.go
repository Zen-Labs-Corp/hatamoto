//go:build integration

package media

import (
	"bytes"
	"context"
	"testing"
)

// Folders are counted by a GROUP BY and the library filter ignores case.
func TestFoldersAndCaseInsensitiveFilter(t *testing.T) {
	repo := scratchMediaRepo(t)
	svc := NewService(repo, Config{UploadDir: t.TempDir(), BaseURL: "/media", MaxSizeMB: 20}, quiet())
	bg := context.Background()
	png := pngBytes(t, 8, 8)
	ids := 0
	for range 3 {
		m, err := svc.Upload(bg, nil, bytes.NewReader(png), "a.png", "", int64(len(png)))
		if err != nil {
			t.Fatal(err)
		}
		ids++
		f := "Photos"
		if ids == 3 {
			f = "photos" // reuses the existing spelling
		}
		if _, err := svc.Update(bg, m.ID, &UpdateMediaInput{Folder: &f}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Upload(bg, nil, bytes.NewReader(png), "b.png", "", int64(len(png))); err != nil {
		t.Fatal(err)
	}
	fs, err := svc.Folders(bg)
	if err != nil || len(fs) != 2 || fs[0] != (FolderCount{"/", 1}) || fs[1] != (FolderCount{"/Photos", 3}) {
		t.Fatalf("Folders = %+v %v", fs, err)
	}
	for _, q := range []string{"Photos", "/photos", "PHOTOS"} {
		res, err := svc.List(bg, &ListMediaFilter{Folder: q}, 50, 0)
		if err != nil || res.Total != 3 {
			t.Errorf("filter %q: %+v %v", q, res, err)
		}
	}
	if res, err := svc.List(bg, &ListMediaFilter{Folder: "/"}, 50, 0); err != nil || res.Total != 1 {
		t.Errorf("uncategorised: %+v %v", res, err)
	}
}

// Folder values stored before the naming rules stay filterable.
func TestLegacyFolderValuesFilterable(t *testing.T) {
	repo := scratchMediaRepo(t)
	svc := NewService(repo, Config{UploadDir: t.TempDir(), BaseURL: "/media", MaxSizeMB: 20}, quiet())
	bg := context.Background()
	png := pngBytes(t, 8, 8)
	for _, legacy := range []string{"/2024/summer", "Logos"} {
		m, err := svc.Upload(bg, nil, bytes.NewReader(png), "a.png", "", int64(len(png)))
		if err != nil {
			t.Fatal(err)
		}
		m.Folder = legacy
		if err := repo.Update(bg, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{"/2024/summer", "2024/summer", "Logos", "/logos"} {
		if res, err := svc.List(bg, &ListMediaFilter{Folder: q}, 50, 0); err != nil || res.Total != 1 {
			t.Errorf("filter %q: %+v %v", q, res, err)
		}
	}
}
