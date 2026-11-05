package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeRepo keeps records in memory so service behaviour (folders, replace) can
// be tested without a database.
type fakeRepo struct {
	created *Media
	items   map[uuid.UUID]*Media
}

func (f *fakeRepo) Create(_ context.Context, m *Media) error {
	f.created = m
	if f.items == nil {
		f.items = map[uuid.UUID]*Media{}
	}
	c := *m
	f.items[m.ID] = &c
	return nil
}
func (f *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*Media, error) {
	m, ok := f.items[id]
	if !ok {
		return nil, ErrMediaNotFound
	}
	c := *m
	return &c, nil
}
func (f *fakeRepo) List(context.Context, *ListMediaFilter, int, int) (*PagedResult, error) {
	return nil, nil
}
func (f *fakeRepo) Update(_ context.Context, m *Media) error {
	c := *m
	f.items[m.ID] = &c
	return nil
}
func (f *fakeRepo) UpdateFile(_ context.Context, m *Media) error {
	old, ok := f.items[m.ID]
	if !ok {
		return ErrMediaNotFound
	}
	old.MimeType, old.FileSize, old.Width, old.Height, old.Variants = m.MimeType, m.FileSize, m.Width, m.Height, m.Variants
	m.UpdatedAt = time.Now()
	old.UpdatedAt = m.UpdatedAt
	return nil
}
func (f *fakeRepo) Delete(context.Context, uuid.UUID) error { return nil }
func (f *fakeRepo) ListImagesAfter(context.Context, uuid.UUID, int) ([]*Media, error) {
	return nil, nil
}
func (f *fakeRepo) CountImages(context.Context) (int, error) { return 0, nil }
func (f *fakeRepo) Folders(context.Context) ([]FolderCount, error) {
	counts := map[string]int{}
	for _, m := range f.items {
		counts[m.Folder]++
	}
	var out []FolderCount
	for name, n := range counts {
		out = append(out, FolderCount{Name: name, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func newTestService(t *testing.T, cfg Config) *Service {
	t.Helper()
	cfg.UploadDir = t.TempDir()
	if cfg.BaseURL == "" {
		cfg.BaseURL = "/media"
	}
	if cfg.MaxSizeMB == 0 {
		cfg.MaxSizeMB = 10
	}
	return NewService(&fakeRepo{}, cfg, slog.Default())
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		img.Set(x, 0, color.RGBA{uint8(x), 0, 0, 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtensionMatchesMIME(t *testing.T) {
	cases := []struct {
		ext, mime string
		want      bool
	}{
		{".jpg", "image/jpeg", true},
		{".jpeg", "image/jpeg", true},
		{".JPG", "image/jpeg", true}, // case-insensitive
		{".png", "image/png", true},
		{".png", "image/jpeg", false},   // png name, jpeg bytes
		{".php", "image/jpeg", false},   // renamed script
		{".svg", "image/svg+xml", true}, // svg is stored after sanitising
		{".png", "image/svg+xml", false},
		{".exe", "application/octet-stream", false},
		{".pdf", "application/pdf", true},
	}
	for _, c := range cases {
		if got := ExtensionMatchesMIME(c.ext, c.mime); got != c.want {
			t.Errorf("ExtensionMatchesMIME(%q,%q)=%v want %v", c.ext, c.mime, got, c.want)
		}
	}
}

func TestUpload_PixelCapRejectsOversizeImage(t *testing.T) {
	// A real 20x20 image with the cap set to 100 pixels => 400 > 100 => reject.
	svc := newTestService(t, Config{MaxPixels: 100})
	data := pngBytes(t, 20, 20)
	_, err := svc.Upload(context.Background(), nil, bytes.NewReader(data), "big.png", "image/png", int64(len(data)))
	if err != ErrImageTooLarge {
		t.Fatalf("expected ErrImageTooLarge, got %v", err)
	}
	// The rejected file must not be left on disk.
	entries, _ := os.ReadDir(filepath.Join(svc.config.UploadDir, legacyMediaTenantDir))
	if len(entries) != 0 {
		t.Errorf("expected no stored file after rejection, found %d", len(entries))
	}
}

func TestUpload_WithinPixelCapSucceeds(t *testing.T) {
	svc := newTestService(t, Config{MaxPixels: defaultMaxPixels})
	data := pngBytes(t, 32, 32)
	m, err := svc.Upload(context.Background(), nil, bytes.NewReader(data), "ok.png", "image/png", int64(len(data)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Width == nil || *m.Width != 32 {
		t.Errorf("expected width 32, got %v", m.Width)
	}
}

func TestCreateVariants_PostDecodeBoundsGuard(t *testing.T) {
	// The post-decode bounds guard in createVariants is the backstop for the
	// extended-WebP case where the header (DecodeConfig) understates the real
	// decoded size. Exercise it directly: a genuine 40x40 image (1600 px) with
	// the cap at 1500 must be rejected once decoded.
	svc := newTestService(t, Config{MaxPixels: 1500})
	dir := filepath.Join(svc.config.UploadDir, legacyMediaTenantDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "src.png")
	if err := os.WriteFile(p, pngBytes(t, 40, 40), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Media{Filename: "src.png", StoragePath: filepath.Join(legacyMediaTenantDir, "src.png"), MimeType: "image/png"}
	_, err := svc.createVariants(context.Background(), m, p)
	if err != ErrImageTooLarge {
		t.Fatalf("expected ErrImageTooLarge from post-decode guard, got %v", err)
	}
}

func buildVP8WebP(t *testing.T, w, h int) []byte {
	t.Helper()
	// Minimal RIFF/WEBP with a lossy "VP8 " chunk carrying w/h in the 14-bit
	// fields the parser reads (offset 6..10 of the chunk payload).
	payload := make([]byte, 10)
	binary.LittleEndian.PutUint16(payload[6:8], uint16(w&0x3FFF))
	binary.LittleEndian.PutUint16(payload[8:10], uint16(h&0x3FFF))
	var b []byte
	b = append(b, []byte("RIFF")...)
	sizePos := len(b)
	b = append(b, 0, 0, 0, 0) // riff size (unused by parser)
	b = append(b, []byte("WEBP")...)
	b = append(b, []byte("VP8 ")...)
	cs := make([]byte, 4)
	binary.LittleEndian.PutUint32(cs, uint32(len(payload)))
	b = append(b, cs...)
	b = append(b, payload...)
	binary.LittleEndian.PutUint32(b[sizePos:sizePos+4], uint32(len(b)-8))
	return b
}

func TestWebpFramePixels_ReadsFrameSize(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.webp")
	if err := os.WriteFile(p, buildVP8WebP(t, 12000, 12000), 0o644); err != nil {
		t.Fatal(err)
	}
	px, ok := webpFramePixels(p)
	if !ok {
		t.Fatal("expected to parse webp frame dims")
	}
	if px != int64(12000)*12000 {
		t.Errorf("got %d px, want %d", px, 12000*12000)
	}
}

func TestEnforcePixelCap_RejectsWebpBombBeforeDecode(t *testing.T) {
	// A tiny WebP whose frame claims 12000x12000 (144MP) must be rejected by
	// the header parser, before any decode allocates.
	svc := newTestService(t, Config{MaxPixels: defaultMaxPixels})
	dir := filepath.Join(svc.config.UploadDir, legacyMediaTenantDir)
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "bomb.webp")
	os.WriteFile(p, buildVP8WebP(t, 12000, 12000), 0o644)
	if err := svc.enforcePixelCap(p); err != ErrImageTooLarge {
		t.Fatalf("expected ErrImageTooLarge from webp header check, got %v", err)
	}
}

func buildPaddedWebPBomb(t *testing.T, junkBytes, w, h int) []byte {
	t.Helper()
	var b []byte
	b = append(b, []byte("RIFF")...)
	sizePos := len(b)
	b = append(b, 0, 0, 0, 0)
	b = append(b, []byte("WEBP")...)
	// VP8X canvas header (small, to fool a canvas-based check).
	b = appendChunk(b, "VP8X", make([]byte, 10))
	// A big JUNK chunk to push the frame past any fixed read window.
	b = appendChunk(b, "JUNK", make([]byte, junkBytes))
	// The real lossy frame claiming w x h.
	payload := make([]byte, 10)
	binary.LittleEndian.PutUint16(payload[6:8], uint16(w&0x3FFF))
	binary.LittleEndian.PutUint16(payload[8:10], uint16(h&0x3FFF))
	b = appendChunk(b, "VP8 ", payload)
	binary.LittleEndian.PutUint32(b[sizePos:sizePos+4], uint32(len(b)-8))
	return b
}

func appendChunk(b []byte, fourcc string, payload []byte) []byte {
	b = append(b, []byte(fourcc)...)
	cs := make([]byte, 4)
	binary.LittleEndian.PutUint32(cs, uint32(len(payload)))
	b = append(b, cs...)
	b = append(b, payload...)
	if len(payload)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

func TestEnforcePixelCap_RejectsPaddedWebPBomb(t *testing.T) {
	// Frame chunk sits AFTER a 70KB junk chunk (past any fixed read window).
	svc := newTestService(t, Config{MaxPixels: defaultMaxPixels})
	dir := filepath.Join(svc.config.UploadDir, legacyMediaTenantDir)
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "padded.webp")
	os.WriteFile(p, buildPaddedWebPBomb(t, 70000, 16383, 16383), 0o644)
	if err := svc.enforcePixelCap(p); err != ErrImageTooLarge {
		t.Fatalf("expected ErrImageTooLarge for padded webp bomb, got %v", err)
	}
}

// Content is checked inside Upload, so no caller (HTTP handler or plugin) can
// store HTML or script under the site's origin, or a type that lies about its
// extension. The caller's mime type is advisory and ignored.
func TestUpload_ValidatesContent(t *testing.T) {
	svc := newTestService(t, Config{MaxPixels: defaultMaxPixels})
	png := pngBytes(t, 8, 8)
	cases := []struct {
		name, filename, mime string
		data                 []byte
	}{
		{"html named html", "x.html", "text/html", []byte("<html><script>alert(1)</script></html>")},
		{"html claiming png", "x.png", "image/png", []byte("<html><script>alert(1)</script></html>")},
		{"html named png with png header lie", "x.html", "image/png", png},
		{"html named svg", "x.svg", "image/svg+xml", []byte("<html><script>alert(1)</script></html>")},
		{"svg bytes named png", "x.png", "image/png", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`)},
		{"jpeg bytes named png", "x.png", "image/png", []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00")},
		{"no extension", "x", "image/png", png},
	}
	for _, c := range cases {
		if _, err := svc.Upload(context.Background(), nil, bytes.NewReader(c.data), c.filename, c.mime, int64(len(c.data))); !errors.Is(err, ErrInvalidMimeType) {
			t.Errorf("%s: err = %v, want ErrInvalidMimeType", c.name, err)
		}
	}
	// The stored type is the sniffed one, whatever the caller said.
	m, err := svc.Upload(context.Background(), nil, bytes.NewReader(png), "ok.png", "text/html", int64(len(png)))
	if err != nil || m.MimeType != "image/png" {
		t.Fatalf("valid png: %+v %v", m, err)
	}
	entries, _ := os.ReadDir(filepath.Join(svc.config.UploadDir, legacyMediaTenantDir))
	if len(entries) < 1 {
		t.Fatal("the valid upload was not stored")
	}
}

func TestUpload_TotalQuotaRefusesBeforeWriting(t *testing.T) {
	data := pngBytes(t, 32, 32)
	svc := newTestService(t, Config{MaxTotalBytes: int64(len(data))*2 + 10})
	up := func() error {
		_, err := svc.Upload(context.Background(), nil, bytes.NewReader(data), "a.png", "image/png", int64(len(data)))
		return err
	}
	if err := up(); err != nil {
		t.Fatalf("first upload: %v", err)
	}
	// The original and its variants now use the quota: the next ones are refused.
	var err error
	for i := 0; i < 5 && err == nil; i++ {
		err = up()
	}
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
	if used := dirSize(svc.uploadRoot()); used > svc.config.MaxTotalBytes {
		t.Errorf("disk use %d is over the quota %d", used, svc.config.MaxTotalBytes)
	}
	// A body longer than it declared cannot slip past the quota either.
	svc2 := newTestService(t, Config{MaxTotalBytes: 100})
	if _, err := svc2.Upload(context.Background(), nil, bytes.NewReader(data), "b.png", "image/png", 1); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("understated size: %v", err)
	}
	if dirSize(svc2.uploadRoot()) != 0 {
		t.Error("a refused upload left a file")
	}
}

func TestUpload_NoQuotaByDefault(t *testing.T) {
	svc := newTestService(t, Config{})
	data := pngBytes(t, 32, 32)
	for i := 0; i < 3; i++ {
		if _, err := svc.Upload(context.Background(), nil, bytes.NewReader(data), "a.png", "image/png", int64(len(data))); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUpload_SVGIsStoredSanitised(t *testing.T) {
	svc := newTestService(t, Config{MaxPixels: defaultMaxPixels})
	in := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" onload="x()"><script>alert(1)</script><rect width="4" height="4"/></svg>`)
	m, err := svc.Upload(context.Background(), nil, bytes.NewReader(in), "logo.svg", "text/html", int64(len(in)))
	if err != nil {
		t.Fatal(err)
	}
	if m.MimeType != "image/svg+xml" || len(m.Variants) != 0 || m.Width != nil {
		t.Errorf("unexpected record: %+v", m)
	}
	if len(m.Cleaned) != 2 {
		t.Errorf("Cleaned = %v, want the script and the onload", m.Cleaned)
	}
	stored, err := os.ReadFile(filepath.Join(svc.config.UploadDir, m.StoragePath))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("script")) || bytes.Contains(stored, []byte("onload")) || !bytes.Contains(stored, []byte("<rect")) {
		t.Errorf("stored bytes not sanitised: %s", stored)
	}
	if m.FileSize != int64(len(stored)) {
		t.Errorf("FileSize %d != stored %d", m.FileSize, len(stored))
	}

	bad := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	if _, err := svc.Upload(context.Background(), nil, bytes.NewReader(bad), "x.svg", "", int64(len(bad))); !errors.Is(err, ErrInvalidSVG) {
		t.Errorf("empty-after-clean svg: %v", err)
	}
}

func TestSniffUploadSVG(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"<svg xmlns='x'/>", "image/svg+xml"},
		{"\xef\xbb\xbf  <?xml version='1.0'?>\n<!-- c -->\n<svg>", "image/svg+xml"},
		{"<svgfoo>", "text/html; charset=utf-8"},
		{"<html><svg>", "text/html; charset=utf-8"},
	} {
		if got := SniffUpload([]byte(c.in)); got != c.want && !(c.want != "image/svg+xml" && got != "image/svg+xml") {
			t.Errorf("SniffUpload(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
