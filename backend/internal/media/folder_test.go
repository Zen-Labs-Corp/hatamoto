package media

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeFolder(t *testing.T) {
	for _, c := range []struct {
		in, want string
		bad      bool
	}{
		{"", "/", false},
		{"/", "/", false},
		{"  ", "/", false},
		{"Photos", "/Photos", false},
		{"/Photos", "/Photos", false},
		{" Summer 2024 ", "/Summer 2024", false},
		{"a-b_c", "/a-b_c", false},
		{"Café", "/Café", false},
		{"/a/b", "", true},
		{"a/b", "", true},
		{"a/", "", true},
		{"//a", "", true},
		{"a.b", "", true},
		{"<b>", "", true},
		{"..", "", true},
		{strings.Repeat("a", 60), "/" + strings.Repeat("a", 60), false},
		{strings.Repeat("a", 61), "", true},
	} {
		got, err := NormalizeFolder(c.in)
		if c.bad {
			if !errors.Is(err, ErrInvalidFolder) {
				t.Errorf("%q: want ErrInvalidFolder, got %q %v", c.in, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q = %q %v, want %q", c.in, got, err, c.want)
		}
	}
}

func TestUpdateFolderReusesExistingSpellingAndRefusesNesting(t *testing.T) {
	svc := newTestService(t, Config{})
	ctx := context.Background()
	data := pngBytes(t, 8, 8)
	up := func(name string) *Media {
		m, err := svc.Upload(ctx, nil, bytes.NewReader(data), name, "", int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	a, b := up("a.png"), up("b.png")
	set := func(id *Media, f string) (*Media, error) {
		return svc.Update(ctx, id.ID, &UpdateMediaInput{Folder: &f})
	}
	if m, err := set(a, "Photos"); err != nil || m.Folder != "/Photos" {
		t.Fatalf("first move: %+v %v", m, err)
	}
	if m, err := set(b, "pHOTOS"); err != nil || m.Folder != "/Photos" {
		t.Fatalf("a differently cased name must reuse the existing one: %+v %v", m, err)
	}
	if _, err := set(b, "/Photos/2024"); !errors.Is(err, ErrInvalidFolder) {
		t.Fatalf("second level: %v", err)
	}
	if m, err := set(b, ""); err != nil || m.Folder != "/" {
		t.Fatalf("back to uncategorised: %+v %v", m, err)
	}
	fs, err := svc.Folders(ctx)
	if err != nil || len(fs) != 2 || fs[0] != (FolderCount{"/", 1}) || fs[1] != (FolderCount{"/Photos", 1}) {
		t.Fatalf("Folders = %+v %v", fs, err)
	}
}

// Folder values stored before the naming rules existed stay usable.
func TestLegacyFolderValuesStayUsable(t *testing.T) {
	svc := newTestService(t, Config{})
	ctx := context.Background()
	data := pngBytes(t, 8, 8)
	m, err := svc.Upload(ctx, nil, bytes.NewReader(data), "a.png", "", int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	repo := svc.repo.(*fakeRepo)
	repo.items[m.ID].Folder = "/2024/summer"
	if _, err := svc.List(ctx, &ListMediaFilter{Folder: "/2024/summer"}, 10, 0); err != nil {
		t.Fatalf("filter on a legacy folder: %v", err)
	}
	same := "/2024/summer"
	if got, err := svc.Update(ctx, m.ID, &UpdateMediaInput{Folder: &same}); err != nil || got.Folder != "/2024/summer" {
		t.Fatalf("re-save: %+v %v", got, err)
	}
	if _, err := svc.Update(ctx, m.ID, &UpdateMediaInput{Folder: &[]string{"/2025/new"}[0]}); !errors.Is(err, ErrInvalidFolder) {
		t.Fatalf("a new nested name must still be refused: %v", err)
	}
	repo.items[m.ID].Folder = "Logos"
	logos := "/logos"
	if got, err := svc.Update(ctx, m.ID, &UpdateMediaInput{Folder: &logos}); err != nil || got.Folder != "Logos" {
		t.Fatalf("slashless legacy value must be reused, not duplicated: %+v %v", got, err)
	}
}
