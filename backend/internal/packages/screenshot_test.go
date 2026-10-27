package packages

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"strings"
	"testing"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// webpHeader is a minimal extended-format WebP header: enough for DecodeConfig.
func webpHeader(w, h int) []byte {
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

func TestValidateScreenshot(t *testing.T) {
	good := pngBytes(t, 1280, 720)
	big := append(bytes.Clone(good), make([]byte, MaxScreenshotBytes)...)
	tests := []struct {
		name, rel string
		files     map[string][]byte
		wantErr   string
	}{
		{"ok png", "assets/shot.png", map[string][]byte{"assets/shot.png": good}, ""},
		{"ok webp", "assets/shot.webp", map[string][]byte{"assets/shot.webp": webpHeader(1280, 720)}, ""},
		{"missing", "assets/shot.png", map[string][]byte{}, "not in the package"},
		{"dotdot", "assets/../shot.png", map[string][]byte{"shot.png": good}, "traversal"},
		{"not under assets", "shot.png", map[string][]byte{"shot.png": good}, "under assets/"},
		{"svg", "assets/shot.svg", map[string][]byte{"assets/shot.svg": []byte("<svg/>")}, ".png, .webp"},
		{"png named webp", "assets/shot.webp", map[string][]byte{"assets/shot.webp": good}, "named"},
		{"oversized bytes", "assets/shot.png", map[string][]byte{"assets/shot.png": big}, "larger than"},
		{"too small", "assets/shot.png", map[string][]byte{"assets/shot.png": pngBytes(t, 100, 100)}, "100x100"},
		{"too big", "assets/shot.png", map[string][]byte{"assets/shot.png": pngBytes(t, 5000, 5000)}, "5000x5000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateScreenshot(tc.rel, tc.files)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestScreenshotExt(t *testing.T) {
	for in, want := range map[string]string{"assets/a.PNG": ".png", "a.jpeg": ".jpg", "a.jpg": ".jpg", "a.webp": ".webp", "a.svg": ""} {
		if got := ScreenshotExt(in); got != want {
			t.Errorf("ScreenshotExt(%q) = %q, want %q", in, got, want)
		}
	}
}

func themePackage(t *testing.T, manifest string, extra map[string][]byte) (*Package, error) {
	t.Helper()
	files := map[string][]byte{ThemeManifest: []byte(manifest)}
	for k, v := range extra {
		files[k] = v
	}
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := PackFiles(files, &key)
	if err != nil {
		return nil, err
	}
	return Parse(raw, DefaultLimits)
}

func TestThemeScreenshotInParse(t *testing.T) {
	shot := map[string][]byte{"assets/shot.png": pngBytes(t, 1280, 720)}
	p, err := themePackage(t, `{"name":"kumo","version":"1.0.0"}`, nil)
	if err != nil {
		t.Fatalf("no screenshot key: %v", err)
	}
	if m, _ := p.ThemeMeta(); m.Screenshot != "" {
		t.Fatalf("Screenshot = %q", m.Screenshot)
	}
	p, err = themePackage(t, `{"name":"kumo","version":"1.0.0","screenshot":"assets/shot.png"}`, shot)
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := p.ThemeMeta(); m.Screenshot != "assets/shot.png" {
		t.Fatalf("Screenshot = %q", m.Screenshot)
	}
	// Parse stays lenient so an installed theme keeps loading: the invalid
	// screenshot is ignored and only recorded in ScreenshotErr.
	for _, rel := range []string{"assets/nope.png", "screenshot.png"} {
		p, err = themePackage(t, `{"name":"kumo","version":"1.0.0","screenshot":"`+rel+`"}`, shot)
		if err != nil {
			t.Fatalf("%s: Parse refused: %v", rel, err)
		}
		if m, _ := p.ThemeMeta(); m.Screenshot != "" || p.ScreenshotErr == nil {
			t.Fatalf("%s: Screenshot = %q, ScreenshotErr = %v", rel, m.Screenshot, p.ScreenshotErr)
		}
	}
}
