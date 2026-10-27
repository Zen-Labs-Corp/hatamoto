package app

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/packages"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 640, 400))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// testWebP is a minimal extended-format WebP header, enough for DecodeConfig.
func testWebP(w, h int) []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(22))
	b.WriteString("WEBPVP8X")
	binary.Write(&b, binary.LittleEndian, uint32(10))
	b.Write([]byte{0, 0, 0, 0})
	b.Write([]byte{byte(w - 1), byte((w - 1) >> 8), byte((w - 1) >> 16)})
	b.Write([]byte{byte(h - 1), byte((h - 1) >> 8), byte((h - 1) >> 16)})
	return b.Bytes()
}

func themeFiles(t *testing.T, name, shot string, img []byte) map[string]string {
	t.Helper()
	manifest := `{"name":"` + name + `","version":"1.0.0"}`
	if shot != "" {
		manifest = `{"name":"` + name + `","version":"1.0.0","screenshot":"` + shot + `"}`
	}
	f := map[string]string{"theme.json": manifest, "layouts/base.html": "x"}
	if shot != "" {
		f[shot] = string(img)
	}
	return f
}

func zipFile(t *testing.T, files map[string]string) (path, sum string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, b := range files {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(b))
	}
	zw.Close()
	h := sha256.Sum256(buf.Bytes())
	path = filepath.Join(t.TempDir(), "a.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, hex.EncodeToString(h[:])
}

func emptyDir(t *testing.T, d string) {
	t.Helper()
	if es, _ := os.ReadDir(d); len(es) != 0 {
		t.Errorf("%s must stay empty, holds %d entries", d, len(es))
	}
}

func TestThemeCheck(t *testing.T) {
	png := testPNG(t)
	good := themeFiles(t, "sunrise", "assets/shot.png", png)
	zp, sum := zipFile(t, good)
	pluginDir := writeDir(t, map[string]string{"plugin.json": `{"name":"hello","version":"1.0.0","sdk":"^0.7"}`, "plugin.wasm": "\x00asm"})
	dir := writeDir(t, good)

	// a packed .hmp to re-read
	hmp := filepath.Join(t.TempDir(), "s.hmp")
	if err := runPack(packages.KindTheme, []string{dir, "--out", hmp}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	noShot := writeDir(t, themeFiles(t, "plain", "", nil))
	def := writeDir(t, themeFiles(t, "default", "assets/shot.png", png))

	cases := []struct {
		name string
		args []string
		want string // error substring, "" for ok
	}{
		{"dir ok", []string{dir}, ""},
		{"dir registry ok", []string{dir, "--registry", "--expect-name", "sunrise"}, ""},
		{"archive ok", []string{"--archive", zp, "--sha256", sum, "--registry"}, ""},
		{"package reread", []string{"--package", hmp, "--registry"}, ""},
		{"sha mismatch", []string{"--archive", zp, "--sha256", strings.Repeat("0", 64)}, "sha256"},
		{"sha not hex", []string{"--archive", zp, "--sha256", "ABC"}, "64 lowercase hex"},
		{"archive without sha", []string{"--archive", zp}, "go together"},
		{"two sources", []string{dir, "--package", hmp}, "exactly one"},
		{"no source", nil, "exactly one"},
		{"registry without screenshot", []string{noShot, "--registry"}, "screenshot"},
		{"no screenshot fine outside registry", []string{noShot}, ""},
		{"default reserved", []string{def, "--registry"}, "default"},
		{"expect-name mismatch", []string{dir, "--expect-name", "other"}, "expected"},
		{"wrong kind", []string{pluginDir}, "not a theme"},
		{"screenshot-out needs registry", []string{dir, "--screenshot-out", t.TempDir()}, "needs --registry"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			err := runThemeCheck(c.args, &out)
			if c.want == "" {
				if err != nil || !strings.HasPrefix(out.String(), "ok: theme ") {
					t.Fatalf("err %v out %q", err, out.String())
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error with %q, got %v", c.want, err)
			}
			if out.Len() != 0 {
				t.Errorf("a refusal prints nothing: %q", out.String())
			}
		})
	}
}

func TestThemeCheckScreenshotOut(t *testing.T) {
	for _, c := range []struct {
		shot string
		img  []byte
		ext  string
	}{{"assets/shot.png", testPNG(t), ".png"}, {"assets/s.webp", testWebP(1280, 720), ".webp"}} {
		dir := writeDir(t, themeFiles(t, "sunrise", c.shot, c.img))
		dest := t.TempDir()
		var out bytes.Buffer
		if err := runThemeCheck([]string{dir, "--registry", "--screenshot-out", dest}, &out); err != nil {
			t.Fatal(err)
		}
		if want := "screenshot: sunrise" + c.ext + "\n"; !strings.HasSuffix(out.String(), want) {
			t.Errorf("stdout %q lacks %q", out.String(), want)
		}
		if got, _ := os.ReadFile(filepath.Join(dest, "sunrise"+c.ext)); !bytes.Equal(got, c.img) {
			t.Errorf("%s: screenshot not written intact", c.ext)
		}
	}
}

func TestThemeCheckRefusalWritesNothing(t *testing.T) {
	dest := t.TempDir()
	noShot := writeDir(t, themeFiles(t, "plain", "", nil))
	if err := runThemeCheck([]string{noShot, "--registry", "--screenshot-out", dest}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected refusal")
	}
	bad := writeDir(t, themeFiles(t, "bad", "assets/shot.png", []byte("not an image")))
	if err := runThemeCheck([]string{bad, "--registry", "--screenshot-out", dest}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected refusal")
	}
	emptyDir(t, dest)
}

func TestThemePackFromArchiveEqualsDirPack(t *testing.T) {
	files := themeFiles(t, "sunrise", "assets/shot.png", testPNG(t))
	files["layouts/page.html"] = "y"
	dir := writeDir(t, files)
	zp, sum := zipFile(t, files)
	keys := t.TempDir()
	if err := runKeysGenerate([]string{"--dir", keys, "--name", "k"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(keys, "k.key")
	fromDir, fromZip, fromHmp := filepath.Join(t.TempDir(), "d.hmp"), filepath.Join(t.TempDir(), "z.hmp"), filepath.Join(t.TempDir(), "h.hmp")
	shots := t.TempDir()
	var out bytes.Buffer
	if err := runPack(packages.KindTheme, []string{dir, "--key", key, "--out", fromDir}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := runPack(packages.KindTheme, []string{"--archive", zp, "--sha256", sum, "--registry", "--expect-name", "sunrise", "--screenshot-out", shots, "--key", key, "--out", fromZip}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.String(), "screenshot: sunrise.png\n") {
		t.Errorf("stdout:\n%s", out.String())
	}
	if err := runPack(packages.KindTheme, []string{"--package", fromDir, "--key", key, "--out", fromHmp}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(fromDir)
	for _, f := range []string{fromZip, fromHmp} {
		if b, _ := os.ReadFile(f); !bytes.Equal(a, b) {
			t.Errorf("%s differs from the dir pack", f)
		}
	}
	if _, err := os.Stat(filepath.Join(shots, "sunrise.png")); err != nil {
		t.Error(err)
	}
}

func TestThemePackRefusalWritesNothing(t *testing.T) {
	files := themeFiles(t, "plain", "", nil)
	zp, sum := zipFile(t, files)
	out := filepath.Join(t.TempDir(), "x.hmp")
	if err := runPack(packages.KindTheme, []string{"--archive", zp, "--sha256", sum, "--registry", "--out", out}, &bytes.Buffer{}); err == nil {
		t.Fatal("a registry pack without screenshot must be refused")
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("the .hmp was written despite the refusal")
	}
}

func TestPluginPackTakesNoThemeFlags(t *testing.T) {
	if err := runPack(packages.KindPlugin, []string{"d", "--registry"}, &bytes.Buffer{}); err == nil {
		t.Error("--registry is a theme flag")
	}
}

func TestThemeCheckRegistryRefusesInvalidScreenshots(t *testing.T) {
	for name, c := range map[string]struct {
		shot string
		data []byte
	}{
		"not under assets": {"screenshot.png", testPNG(t)},
		"missing file":     {"assets/nope.png", nil},
		"not an image":     {"assets/shot.png", []byte("not an image")},
	} {
		files := themeFiles(t, "bad", c.shot, c.data)
		if c.data == nil {
			delete(files, c.shot)
		}
		dir := writeDir(t, files)
		if err := runThemeCheck([]string{dir, "--registry"}, &bytes.Buffer{}); err == nil {
			t.Errorf("%s: --registry accepted it", name)
		}
		if err := runThemeCheck([]string{dir}, &bytes.Buffer{}); err != nil {
			t.Errorf("%s: plain check refused: %v", name, err)
		}
	}
}
