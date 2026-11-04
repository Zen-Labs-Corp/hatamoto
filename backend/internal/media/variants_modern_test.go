package media

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	xwebp "golang.org/x/image/webp"
	"hatamoto.dev/core/internal/media/modern"
)

// variantRepo keeps media records in memory; it answers GetByID and Update.
type variantRepo struct {
	fakeRepo
	byID map[uuid.UUID]*Media
}

func (r *variantRepo) Create(_ context.Context, m *Media) error {
	if r.byID == nil {
		r.byID = map[uuid.UUID]*Media{}
	}
	r.byID[m.ID] = m
	return nil
}

func (r *variantRepo) GetByID(_ context.Context, id uuid.UUID) (*Media, error) {
	if m, ok := r.byID[id]; ok {
		c := *m
		return &c, nil
	}
	return nil, ErrMediaNotFound
}

func (r *variantRepo) Update(_ context.Context, m *Media) error {
	c := *m
	r.byID[m.ID] = &c
	return nil
}

func photoImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx, fy := float64(x)/float64(w), float64(y)/float64(h)
			n := math.Sin(fx*60)*math.Cos(fy*50)*14 + float64((x*7+y*13)%9)
			c := func(v float64) uint8 { return uint8(math.Max(0, math.Min(255, v))) }
			img.SetRGBA(x, y, color.RGBA{c(110 + 110*fx + n), c(90 + 120*fy + n), c(170 - 90*fx*fy + n), 255})
		}
	}
	return img
}

func jpegPhoto(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, photoImage(w, h), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func webpPhoto(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := modern.Encode(context.Background(), &buf, photoImage(w, h), modern.WebP, 90); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newModernService(t *testing.T, pol func(context.Context) Formats) (*Service, *variantRepo) {
	t.Helper()
	repo := &variantRepo{}
	svc := NewService(repo, Config{UploadDir: t.TempDir(), BaseURL: "/media", MaxSizeMB: 20}, slog.Default())
	svc.SetFormatPolicy(pol)
	return svc, repo
}

func both(context.Context) Formats { return Formats{WebP: true, AVIF: true} }

func fileHead(t *testing.T, svc *Service, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(svc.local.localPath(rel))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func variantBy(m *Media, size, format string) *ImageVariant {
	for i := range m.Variants {
		if m.Variants[i].Size == size && m.Variants[i].Format == format {
			return &m.Variants[i]
		}
	}
	return nil
}

func TestUploadJPEGMakesSmallerWebPCopies(t *testing.T) {
	svc, _ := newModernService(t, both)
	m, err := svc.Upload(context.Background(), nil, bytes.NewReader(jpegPhoto(t, 3000, 2000)), "photo.jpg", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []string{"medium", "large"} {
		fb := variantBy(m, size, "")
		w := variantBy(m, size, "image/webp")
		if fb == nil || w == nil {
			t.Fatalf("%s: fallback %v webp %v in %+v", size, fb, w, m.Variants)
		}
		if !strings.HasSuffix(w.Path, ".webp") || !strings.HasSuffix(fb.Path, ".jpg") {
			t.Errorf("%s names: %s %s", size, fb.Path, w.Path)
		}
		head := fileHead(t, svc, w.Path)
		if string(head[:4]) != "RIFF" || string(head[8:12]) != "WEBP" {
			t.Errorf("%s: not WebP", size)
		}
		if len(head) >= len(fileHead(t, svc, fb.Path)) {
			t.Errorf("%s: webp copy (%d) not smaller than the jpeg copy", size, len(head))
		}
		if w.Width != fb.Width || w.Height != fb.Height {
			t.Errorf("%s: modern copy size differs from fallback", size)
		}
	}
	if v := variantBy(m, "thumbnail", "image/webp"); v != nil {
		t.Error("thumbnail must not get a modern copy")
	}
	if variantBy(m, "full", "image/webp") != nil {
		t.Error("a 3000 px source has a large copy, so no full copy")
	}
}

func TestUploadWebPOriginalGetsWebPFallbackNamedWebP(t *testing.T) {
	svc, _ := newModernService(t, both)
	m, err := svc.Upload(context.Background(), nil, bytes.NewReader(webpPhoto(t, 2000, 1500)), "photo.webp", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if m.MimeType != "image/webp" || len(m.Variants) == 0 {
		t.Fatalf("mime %s variants %d", m.MimeType, len(m.Variants))
	}
	for _, v := range m.Variants {
		if strings.HasSuffix(v.Path, ".jpg") {
			t.Errorf("%s: .jpg copy of a WebP original", v.Path)
		}
		switch v.Format {
		case "", "image/webp":
			if !strings.HasSuffix(v.Path, ".webp") {
				t.Errorf("%s should end .webp", v.Path)
			}
			if _, err := xwebp.Decode(bytes.NewReader(fileHead(t, svc, v.Path))); err != nil {
				t.Errorf("%s: %v", v.Path, err)
			}
		case "image/avif":
			if !strings.HasSuffix(v.Path, ".avif") {
				t.Errorf("%s should end .avif", v.Path)
			}
		}
	}
	// WebP original: no second WebP copy, only AVIF extras.
	for _, v := range m.Variants {
		if v.Format == "image/webp" {
			t.Errorf("explicit webp format on a webp original: %+v", v)
		}
	}
}

func TestSmallSourceGetsFullModernCopy(t *testing.T) {
	svc, _ := newModernService(t, func(context.Context) Formats { return Formats{WebP: true} })
	m, err := svc.Upload(context.Background(), nil, bytes.NewReader(jpegPhoto(t, 800, 600)), "p.jpg", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	full := variantBy(m, "full", "image/webp")
	if full == nil || full.Width != 800 || full.Height != 600 {
		t.Fatalf("full copy: %+v in %+v", full, m.Variants)
	}
	if variantBy(m, "large", "") != nil {
		t.Error("no large copy for an 800 px source")
	}
}

func TestModernCopyLargerThanFallbackIsDropped(t *testing.T) {
	// Noise: WebP at quality 80 cannot beat a flat colour PNG copy.
	img := image.NewRGBA(image.Rect(0, 0, 1500, 1500))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 20}); err != nil {
		t.Fatal(err)
	}
	svc, _ := newModernService(t, both)
	m, err := svc.Upload(context.Background(), nil, bytes.NewReader(buf.Bytes()), "flat.jpg", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range m.Variants {
		if v.Format == "" {
			continue
		}
		fb := variantBy(m, v.Size, "")
		if fb == nil || len(fileHead(t, svc, v.Path)) >= len(fileHead(t, svc, fb.Path)) {
			t.Errorf("kept a modern copy that is not smaller: %+v", v)
		}
	}
}

func TestFormatPolicyOffMakesNoModernCopies(t *testing.T) {
	svc, _ := newModernService(t, func(context.Context) Formats { return Formats{} })
	m, err := svc.Upload(context.Background(), nil, bytes.NewReader(jpegPhoto(t, 2000, 1000)), "p.jpg", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range m.Variants {
		if v.Format != "" {
			t.Errorf("modern copy with the policy off: %+v", v)
		}
	}
}

func TestVariantJSONWithoutFormatMeansOriginal(t *testing.T) {
	var v ImageVariant
	if err := json.Unmarshal([]byte(`{"size":"medium","path":"a_medium.jpg","url":"/media/a","width":300,"height":200}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.Format != "" || v.MIME("image/jpeg") != "image/jpeg" {
		t.Fatalf("%+v", v)
	}
	v.Format = "image/webp"
	if v.MIME("image/jpeg") != "image/webp" {
		t.Fatal("format wins")
	}
	b, _ := json.Marshal(ImageVariant{Size: "m"})
	if strings.Contains(string(b), "format") {
		t.Errorf("empty format must be omitted: %s", b)
	}
}

func TestDeleteRemovesModernFilesToo(t *testing.T) {
	svc, repo := newModernService(t, both)
	m, err := svc.Upload(context.Background(), nil, bytes.NewReader(jpegPhoto(t, 2000, 1000)), "p.jpg", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, v := range m.Variants {
		paths = append(paths, v.Path)
	}
	if variantBy(m, "large", "image/webp") == nil {
		t.Fatal("expected a webp copy")
	}
	// Delete wants the repo to serve the record and drop it.
	repo.fakeRepo.created = nil
	if err := svc.Delete(context.Background(), m.ID); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if _, err := os.Stat(svc.local.localPath(p)); !os.IsNotExist(err) {
			t.Errorf("%s still exists", p)
		}
	}
}

func TestRegenerateVariantsReplacesStaleCopies(t *testing.T) {
	// An old WebP upload: JPEG bytes in .jpg-named copies.
	svc, repo := newModernService(t, func(context.Context) Formats { return Formats{} })
	m, err := svc.Upload(context.Background(), nil, bytes.NewReader(webpPhoto(t, 1500, 1000)), "old.webp", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(m.StoragePath)
	stale := filepath.Join(dir, strings.TrimSuffix(m.Filename, ".webp")+"_large.jpg")
	if _, err := svc.store.Put(context.Background(), stale, bytes.NewReader([]byte("jpegbytes"))); err != nil {
		t.Fatal(err)
	}
	repo.byID[m.ID].Variants = append(repo.byID[m.ID].Variants, ImageVariant{Size: "large", Path: stale, Width: 1024, Height: 683})
	before := fileHead(t, svc, m.StoragePath)

	svc.SetFormatPolicy(both)
	got, err := svc.RegenerateVariants(context.Background(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(svc.local.localPath(stale)); !os.IsNotExist(err) {
		t.Error("stale .jpg copy not deleted")
	}
	if variantBy(got, "large", "image/avif") == nil && variantBy(got, "medium", "image/avif") == nil {
		t.Errorf("no avif copy after regenerate: %+v", got.Variants)
	}
	if !bytes.Equal(before, fileHead(t, svc, m.StoragePath)) {
		t.Error("original modified")
	}
	if repo.byID[m.ID].Variants == nil || len(repo.byID[m.ID].Variants) != len(got.Variants) {
		t.Error("variants not persisted")
	}
}
