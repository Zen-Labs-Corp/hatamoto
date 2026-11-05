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

func TestListRefusesAnInvalidFolderFilter(t *testing.T) {
	svc := newTestService(t, Config{})
	if _, err := svc.List(context.Background(), &ListMediaFilter{Folder: "a/b"}, 10, 0); !errors.Is(err, ErrInvalidFolder) {
		t.Fatalf("err = %v", err)
	}
}
