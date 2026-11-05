package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func uploadTest(t *testing.T, svc *Service, name string, data []byte) *Media {
	t.Helper()
	m, err := svc.Upload(context.Background(), nil, bytes.NewReader(data), name, "", int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func replaceTest(svc *Service, m *Media, name string, data []byte) (*Media, error) {
	return svc.Replace(context.Background(), m.ID, bytes.NewReader(data), name, int64(len(data)))
}

func readStored(t *testing.T, svc *Service, key string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(svc.config.UploadDir, key))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestReplaceKeepsTheAddressAndRebuildsVariants(t *testing.T) {
	svc := newTestService(t, Config{MaxPixels: defaultMaxPixels})
	notified := 0
	svc.SetChangeObserver(func(context.Context) { notified++ })
	old := uploadTest(t, svc, "photo.png", pngBytes(t, 400, 300))
	before, _ := svc.repo.GetByID(context.Background(), old.ID)

	fresh := pngBytes(t, 1200, 800)
	got, err := replaceTest(svc, old, "other-name.png", fresh)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != old.ID || got.Filename != old.Filename || got.StoragePath != old.StoragePath ||
		*got.PublicURL != *old.PublicURL || got.OriginalFilename != "photo.png" {
		t.Errorf("identity changed: %+v vs %+v", got, old)
	}
	if !bytes.Equal(readStored(t, svc, old.StoragePath), fresh) || got.FileSize != int64(len(fresh)) {
		t.Error("the new bytes are not served at the same path")
	}
	if *got.Width != 1200 || *got.Height != 800 {
		t.Errorf("dimensions %dx%d", *got.Width, *got.Height)
	}
	var large *ImageVariant
	for i := range got.Variants {
		if got.Variants[i].Size == "large" && got.Variants[i].Format == "" {
			large = &got.Variants[i]
		}
	}
	if large == nil || large.Width != 1024 {
		t.Fatalf("variants not rebuilt from the new image: %+v", got.Variants)
	}
	stored, _ := svc.repo.GetByID(context.Background(), old.ID)
	if stored.FileSize != got.FileSize || !stored.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("row not updated: %+v", stored)
	}
	if notified != 1 {
		t.Errorf("observer fired %d times, want 1", notified)
	}
	entries, _ := os.ReadDir(filepath.Join(svc.config.UploadDir, legacyMediaTenantDir))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "replace-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestReplaceRefusesAnotherKindOrExtension(t *testing.T) {
	svc := newTestService(t, Config{MaxPixels: defaultMaxPixels})
	m := uploadTest(t, svc, "photo.png", pngBytes(t, 8, 8))
	jpeg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00")
	for _, c := range []struct {
		name string
		data []byte
		want error
	}{
		{"x.jpg", jpeg, ErrReplaceKind},                     // another image kind
		{"x.pdf", []byte("%PDF-1.4\n%abc"), ErrReplaceKind}, // another family
		{"x.png", jpeg, ErrInvalidMimeType},                 // the name lies about the content
		{"x.html", []byte("<html></html>"), ErrInvalidMimeType},
	} {
		if _, err := replaceTest(svc, m, c.name, c.data); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	if !bytes.Equal(readStored(t, svc, m.StoragePath), pngBytes(t, 8, 8)) {
		t.Error("a refused replace changed the file")
	}
}

func TestReplaceQuotaCountsTheDifference(t *testing.T) {
	small := pngBytes(t, 8, 8)
	svc := newTestService(t, Config{MaxPixels: defaultMaxPixels, MaxTotalBytes: 1 << 20})
	m := uploadTest(t, svc, "a.png", small)
	used := dirSize(svc.uploadRoot())
	svc.config.MaxTotalBytes = used + 50 // room for a slightly larger file only

	same := pngBytes(t, 8, 8)
	if _, err := replaceTest(svc, m, "a.png", same); err != nil {
		t.Fatalf("same size must fit: %v", err)
	}
	big := pngBytes(t, 300, 300)
	if _, err := replaceTest(svc, m, "a.png", big); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("a much larger file: %v, want ErrQuotaExceeded", err)
	}
	// A body longer than declared cannot pass either.
	if _, err := svc.Replace(context.Background(), m.ID, bytes.NewReader(big), "a.png", 1); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("understated size: %v", err)
	}
}

func TestReplaceSanitisesSVG(t *testing.T) {
	svc := newTestService(t, Config{})
	m := uploadTest(t, svc, "logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="1"/></svg>`))
	dirty := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>x</script><circle r="2" onclick="y()"/></svg>`)
	got, err := replaceTest(svc, m, "new.svg", dirty)
	if err != nil {
		t.Fatal(err)
	}
	b := readStored(t, svc, m.StoragePath)
	if bytes.Contains(b, []byte("script")) || bytes.Contains(b, []byte("onclick")) || !bytes.Contains(b, []byte("<circle")) {
		t.Errorf("stored: %s", b)
	}
	if len(got.Cleaned) != 2 {
		t.Errorf("Cleaned = %v", got.Cleaned)
	}
	if _, err := replaceTest(svc, m, "x.svg", []byte(`<!DOCTYPE svg [<!ENTITY a "b">]><svg/>`)); err == nil {
		t.Error("a hostile svg must be refused")
	}
}

func TestReplaceFailureLeavesTheOldFileIntact(t *testing.T) {
	svc := newTestService(t, Config{MaxPixels: defaultMaxPixels})
	orig := pngBytes(t, 8, 8)
	m := uploadTest(t, svc, "a.png", orig)

	real := createUpload
	createUpload = func(p string) (io.WriteCloser, error) {
		if strings.Contains(p, "replace-") {
			return nil, errors.New("disk full")
		}
		return real(p)
	}
	t.Cleanup(func() { createUpload = real })

	if _, err := replaceTest(svc, m, "a.png", pngBytes(t, 40, 40)); !errors.Is(err, ErrStorageFailed) {
		t.Fatalf("err = %v", err)
	}
	if !bytes.Equal(readStored(t, svc, m.StoragePath), orig) {
		t.Error("the old file was damaged")
	}
	stored, _ := svc.repo.GetByID(context.Background(), m.ID)
	if stored.FileSize != int64(len(orig)) {
		t.Errorf("row changed: %+v", stored)
	}
}

func TestReplaceUnknownID(t *testing.T) {
	svc := newTestService(t, Config{})
	m := &Media{}
	if _, err := replaceTest(svc, m, "a.png", pngBytes(t, 8, 8)); !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("err = %v", err)
	}
}

// failingUpdateFile makes the row update of a Replace fail.
type failingUpdateFile struct{ Repository }

func (failingUpdateFile) UpdateFile(context.Context, *Media) error { return errors.New("db down") }

func TestReplaceRowFailureRestoresTheOldCopies(t *testing.T) {
	svc := newTestService(t, Config{MaxPixels: defaultMaxPixels})
	m := uploadTest(t, svc, "a.png", pngBytes(t, 1200, 800))
	if len(m.Variants) == 0 {
		t.Fatal("no variants to protect")
	}
	before := map[string][]byte{}
	for _, v := range m.Variants {
		before[v.Path] = readStored(t, svc, v.Path)
	}
	svc.repo = failingUpdateFile{svc.repo}
	if _, err := replaceTest(svc, m, "a.png", pngBytes(t, 1300, 900)); err == nil {
		t.Fatal("want the row failure")
	}
	for p, want := range before {
		if got, err := os.ReadFile(filepath.Join(svc.config.UploadDir, p)); err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s was not restored: %v", p, err)
		}
	}
}
