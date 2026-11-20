package portability

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/media"
)

func TestCheckMediaRowRejectsUnsafePaths(t *testing.T) {
	bad := []string{
		`{"storage_path":"../../../../tmp/hm34x/victim"}`, // the reviewer's payload
		`{"storage_path":"/etc/passwd"}`,
		`{"storage_path":"a/../../b"}`,
		`{"storage_path":"..\\..\\x"}`,
		`{"storage_path":""}`,
		`{"storage_path":"ok.png","variants":[{"path":"../escape.png"}]}`,
		`{"storage_path":"ok.png","variants":[{"path":"/abs.png"}]}`,
		`{"storage_path":"ok.png","storage_provider":"gcs"}`,
	}
	for _, row := range bad {
		if err := checkMediaRow([]byte(row)); err == nil {
			t.Errorf("accepted %s", row)
		}
	}
	good := []string{
		`{"storage_path":"tenant/pic.png","storage_provider":"local","variants":[{"path":"tenant/pic_thumbnail.png"},{"path":""}]}`,
		`{"storage_path":"pic.png"}`,
		`{"storage_path":"pic.png","storage_provider":"s3"}`,
	}
	for _, row := range good {
		if err := checkMediaRow([]byte(row)); err != nil {
			t.Errorf("rejected %s: %v", row, err)
		}
	}
}

// A failed import must not delete (or overwrite) files that were already in
// the media directory: the keep.png case from the review.
// pngMagic is enough for the content sniffer to call a file image/png.
const pngMagic = "\x89PNG\r\n\x1a\n"

func TestMediaStageSkipsNonAllowlistedTypes(t *testing.T) {
	st := &mediaStage{root: t.TempDir()}
	defer st.cleanup()
	for name, body := range map[string]string{
		"x.html":     "<!doctype html><script>alert(1)</script>",
		"y.svg":      "<svg xmlns='http://www.w3.org/2000/svg'><script/></svg>",
		"z.png":      "<html><body>not a png</body></html>", // allowed extension, wrong bytes
		"w.txt":      "plain text",
		"page.html ": "<html>",
	} {
		if _, warn, err := st.add(name, strings.NewReader(body)); err != nil || warn == "" {
			t.Errorf("archive file %q: warn=%q err=%v, want a skip warning", name, warn, err)
		}
	}
	if len(st.staged) != 0 {
		t.Errorf("staged %v", st.staged)
	}
}

func TestMediaStageKeepsExistingFiles(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "keep.png")
	if err := os.WriteFile(keep, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := &mediaStage{root: root}
	if _, _, err := st.add("keep.png", strings.NewReader(pngMagic+"from archive")); err == nil {
		t.Fatal("an existing file was accepted for overwrite")
	}
	st.cleanup()

	// A new file is staged, placed, then undone: only it disappears.
	st = &mediaStage{root: root}
	if _, _, err := st.add("new/pic.png", strings.NewReader(pngMagic)); err != nil {
		t.Fatal(err)
	}
	if err := st.place(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "new", "pic.png")); string(b) != pngMagic {
		t.Fatal("file not placed")
	}
	st.undo()
	st.cleanup()
	if _, err := os.Stat(filepath.Join(root, "new", "pic.png")); !os.IsNotExist(err) {
		t.Error("placed file survived undo")
	}
	if b, _ := os.ReadFile(keep); string(b) != "original" {
		t.Errorf("keep.png = %q after the failed import", b)
	}
	if ents, _ := filepath.Glob(filepath.Join(root, ".import-*")); len(ents) != 0 {
		t.Errorf("staging dir left behind: %v", ents)
	}
}

func TestMediaStageRefusesBadNamesAndSymlinkEscapes(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	st := &mediaStage{root: root}
	defer st.cleanup()
	for _, name := range []string{"../x", "/abs", "a/../../x", ".."} {
		if _, _, err := st.add(name, strings.NewReader("x")); err == nil {
			t.Errorf("accepted entry name %q", name)
		}
	}
	if _, _, err := st.add("a.png", strings.NewReader(pngMagic+"1")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.add("a.png", strings.NewReader(pngMagic+"2")); err == nil {
		t.Error("duplicate entry accepted")
	}

	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	esc := &mediaStage{root: root}
	defer esc.cleanup()
	if _, _, err := esc.add("link/victim.png", strings.NewReader(pngMagic)); err != nil {
		t.Fatal(err)
	}
	if err := esc.place(); err == nil {
		t.Error("a symlinked directory leading outside the media dir was followed")
	}
	if _, err := os.Stat(filepath.Join(outside, "victim.txt")); !os.IsNotExist(err) {
		t.Error("file landed outside the media directory")
	}
}

func TestReadLineCapsRowLength(t *testing.T) {
	long := strings.Repeat("x", maxRowBytes+10) + "\n"
	if _, err := readLine(bufio.NewReaderSize(strings.NewReader(long), 4096)); err == nil {
		t.Error("over-long row accepted")
	}
	got, err := readLine(bufio.NewReaderSize(strings.NewReader(strings.Repeat("y", 10000)+"\nrest"), 4096))
	if err != nil || len(got) != 10001 {
		t.Errorf("long-but-fine row: len %d, %v", len(got), err)
	}
}

// Resized copies of WebP uploads from older versions are JPEG bytes named
// .webp; the site's own export must restore. Mismatches that are not two
// allowed images are still skipped.
func TestMediaStageAcceptsJPEGBytesNamedWebP(t *testing.T) {
	st := &mediaStage{root: t.TempDir()}
	defer st.cleanup()
	jpeg := "\xff\xd8\xff\xe0\x00\x10JFIF\x00"
	if _, warn, err := st.add("default/a_thumbnail.webp", strings.NewReader(jpeg)); err != nil || warn != "" {
		t.Fatalf("jpeg-in-webp: warn=%q err=%v", warn, err)
	}
	if _, warn, _ := st.add("default/b.html", strings.NewReader(pngMagic)); warn == "" {
		t.Errorf("png bytes named .html were not skipped")
	}
	if len(st.staged) != 1 {
		t.Errorf("staged %v, want only the webp-named jpeg", st.staged)
	}
}

func TestMediaStageAcceptsAVIFCopies(t *testing.T) {
	st := &mediaStage{root: t.TempDir()}
	defer st.cleanup()
	avif := "\x00\x00\x00\x1cftypavif\x00\x00\x00\x00avifmif1miaf"
	if _, warn, err := st.add("default/a_large.avif", strings.NewReader(avif)); err != nil || warn != "" {
		t.Fatalf("avif copy: warn=%q err=%v", warn, err)
	}
	if _, warn, _ := st.add("default/b.png", strings.NewReader(avif)); warn != "" {
		t.Errorf("an AVIF copy under another image extension is still an allowed image pair, got %q", warn)
	}
}

func TestMediaStageSanitisesSVG(t *testing.T) {
	root := t.TempDir()
	st := &mediaStage{root: root}
	defer st.cleanup()
	dirty := `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script><rect width="1" onclick="x()"/></svg>`
	n, warn, err := st.add("ok.svg", strings.NewReader(dirty))
	if err != nil || warn != "" || n != int64(len(dirty)) {
		t.Fatalf("n=%d (want the original %d) warn=%q err=%v", n, len(dirty), warn, err)
	}
	b, err := os.ReadFile(filepath.Join(st.dir, "ok.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "script") || strings.Contains(string(b), "onclick") || !strings.Contains(string(b), "<rect") {
		t.Errorf("staged svg is not clean: %s", b)
	}
	bomb := `<!DOCTYPE svg [<!ENTITY a "aaaa">]><svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`
	if n, warn, err := st.add("bomb.svg", strings.NewReader(bomb)); err != nil || warn == "" || n != int64(len(bomb)) {
		t.Errorf("refused svg: n=%d warn=%q err=%v", n, warn, err)
	}
	if len(st.staged) != 1 {
		t.Errorf("staged %v, want only ok.svg", st.staged)
	}
}

func TestPrepareRowMediaProviderFollowsTarget(t *testing.T) {
	for _, tc := range []struct{ source, target string }{{"s3", "local"}, {"local", "s3"}, {"s3", "s3"}} {
		line := []byte(`{"id":"1","storage_path":"pic.png","storage_provider":"` + tc.source + `"}`)
		got, err := prepareRow(table{name: "hatamoto_media"}, line, tc.target)
		if err != nil {
			t.Fatal(err)
		}
		if want := `"storage_provider":"` + tc.target + `"`; !strings.Contains(got, want) {
			t.Errorf("%s -> %s: row = %s", tc.source, tc.target, got)
		}
	}
}

func TestMediaProvider(t *testing.T) {
	if got := mediaProvider(ImportOptions{}); got != "local" {
		t.Errorf("no store = %q, want local", got)
	}
	if got := mediaProvider(ImportOptions{Store: &media.S3Store{}}); got != "s3" {
		t.Errorf("store set = %q, want s3", got)
	}
}

func TestSecretGate(t *testing.T) {
	withComments := &Header{Tables: []string{"hatamoto_entities", "hatamoto_commenters"}, SecretCheck: SecretCheck("a")}
	noComments := &Header{Tables: []string{"hatamoto_entities"}, SecretCheck: SecretCheck("a")}

	if n, d, err := secretGate(noComments, ImportOptions{Secret: "b"}); n != "" || d || err != nil {
		t.Errorf("no comment tables: %q %v %v", n, d, err)
	}
	if n, d, err := secretGate(withComments, ImportOptions{Secret: "a"}); n != "" || d || err != nil {
		t.Errorf("match: %q %v %v", n, d, err)
	}
	old := &Header{Tables: withComments.Tables}
	if n, d, err := secretGate(old, ImportOptions{Secret: "a"}); !strings.Contains(n, "no server secret check") || d || err != nil {
		t.Errorf("no check: %q %v %v", n, d, err)
	}
	if n, d, err := secretGate(withComments, ImportOptions{}); !strings.Contains(n, "not verified") || d || err != nil {
		t.Errorf("unknown: %q %v %v", n, d, err)
	}
	if _, _, err := secretGate(withComments, ImportOptions{Secret: "b"}); !errors.Is(err, ErrSecretMismatch) {
		t.Errorf("mismatch: %v", err)
	}
	if n, d, err := secretGate(withComments, ImportOptions{Secret: "b", Force: true}); n != "" || !d || err != nil {
		t.Errorf("force: %q %v %v", n, d, err)
	}
}

func headerArchive(t *testing.T, body string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "format.json", Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte(body))
	tw.Close()
	gz.Close()
	return &buf
}

// The gate runs on the header alone: a nil pool would panic on Begin.
func TestImportRefusesSecretMismatchBeforeTx(t *testing.T) {
	body := fmt.Sprintf(`{"format":"hatamoto-export","version":2,"secret_check":%q,"tables":["hatamoto_commenters"]}`, SecretCheck("a"))
	_, err := Import(context.Background(), nil, headerArchive(t, body), ImportOptions{Secret: "other"})
	if !errors.Is(err, ErrSecretMismatch) {
		t.Fatalf("err = %v", err)
	}
}

func TestReadHeaderVersions(t *testing.T) {
	read := func(body string) error {
		gz, err := gzip.NewReader(headerArchive(t, body))
		if err != nil {
			t.Fatal(err)
		}
		_, err = readHeader(tar.NewReader(gz))
		return err
	}
	err := read(`{"format":"hatamoto-export","version":99,"tables":[]}`)
	if err == nil || !strings.Contains(err.Error(), "v99 is newer than this hatamoto supports (v2); upgrade first") {
		t.Errorf("v99: %v", err)
	}
	if err := read(`{"format":"hatamoto-export","version":1,"tables":[]}`); err != nil {
		t.Errorf("v1: %v", err)
	}
}
