package portability

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		`{"storage_path":"ok.png","storage_provider":"s3"}`,
	}
	for _, row := range bad {
		if err := checkMediaRow([]byte(row)); err == nil {
			t.Errorf("accepted %s", row)
		}
	}
	good := []string{
		`{"storage_path":"tenant/pic.png","storage_provider":"local","variants":[{"path":"tenant/pic_thumbnail.png"},{"path":""}]}`,
		`{"storage_path":"pic.png"}`,
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
