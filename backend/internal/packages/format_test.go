package packages

import (
	"archive/zip"
	"bytes"
	"errors"
	"io/fs"
	"strings"
	"testing"
)

const pluginJSON = `{"name":"hello","version":"1.0.0","sdk":"^0.7"}`
const themeJSON = `{"name":"sunrise","version":"1.2.3","settings_schema":{"type":"object","properties":{"accent":{"type":"string"}}}}`

func pluginFiles() map[string][]byte {
	return map[string][]byte{PluginManifest: []byte(pluginJSON), PluginWasm: []byte("\x00asm\x01\x00\x00\x00")}
}

func themeFiles() map[string][]byte {
	return map[string][]byte{
		ThemeManifest:         []byte(themeJSON),
		"layouts/base.html":   []byte("<html></html>"),
		"assets/css/base.css": []byte("body{}"),
	}
}

// rawZip writes entries exactly as given, so a test can build what PackFiles
// would never produce.
func rawZip(t testing.TB, entries ...*zip.FileHeader) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, orig := range entries {
		h := *orig
		body := h.Comment // the body travels in Comment; it must not reach the archive as one
		h.Comment = ""
		w, err := zw.CreateHeader(&h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func hdr(name, body string) *zip.FileHeader {
	return &zip.FileHeader{Name: name, Method: zip.Deflate, Comment: body}
}

func mustRefuse(t *testing.T, raw []byte, want string) {
	t.Helper()
	_, err := Parse(raw, DefaultLimits)
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("Parse err = %v, want a ParseError containing %q", err, want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("Parse err = %q, want it to contain %q", err, want)
	}
}

func TestParsePluginAndTheme(t *testing.T) {
	raw, err := PackFiles(pluginFiles(), nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(raw, DefaultLimits)
	if err != nil || p.Kind != KindPlugin || p.Name != "hello" || p.Version != "1.0.0" || p.Signature != nil {
		t.Fatalf("plugin: %+v, %v", p, err)
	}
	raw, _ = PackFiles(themeFiles(), nil)
	p, err = Parse(raw, DefaultLimits)
	if err != nil || p.Kind != KindTheme || p.Name != "sunrise" {
		t.Fatalf("theme: %+v, %v", p, err)
	}
	if b, err := p.ReadFile("assets/css/base.css"); err != nil || string(b) != "body{}" {
		t.Fatalf("ReadFile = %q, %v", b, err)
	}
}

func TestPackIsDeterministic(t *testing.T) {
	a, _ := PackFiles(themeFiles(), nil)
	b, _ := PackFiles(themeFiles(), nil)
	if !bytes.Equal(a, b) {
		t.Fatal("two packs of the same files differ")
	}
}

func TestHostileArchivesAreRefused(t *testing.T) {
	good := hdr(PluginManifest, pluginJSON)
	tests := []struct {
		name string
		raw  []byte
		want string
	}{
		{"zip slip", rawZip(t, good, hdr("../../etc/passwd", "x")), "traversal"},
		{"nested slip", rawZip(t, good, hdr("assets/../../x", "x")), "traversal"},
		{"absolute", rawZip(t, good, hdr("/etc/passwd", "x")), "absolute"},
		{"backslash", rawZip(t, good, hdr(`assets\..\x`, "x")), "backslash"},
		{"drive letter", rawZip(t, good, hdr("C:/x", "x")), "backslash, colon"},
		{"nul", rawZip(t, good, hdr("assets/a\x00b", "x")), "control"},
		{"hidden", rawZip(t, good, hdr("assets/.env", "x")), "hidden"},
		{"unknown root file", rawZip(t, good, hdr("run.sh", "x")), "root"},
		{"unknown dir", rawZip(t, good, hdr("bin/run", "x")), "directory"},
		{"duplicate", rawZip(t, good, hdr("assets/a", "1"), hdr("assets/a", "2")), "duplicate"},
		{"case duplicate", rawZip(t, good, hdr("assets/a", "1"), hdr("assets/A", "2")), "duplicate"},
		{"two manifests", rawZip(t, good, hdr(ThemeManifest, themeJSON), hdr(PluginWasm, "x")), "exactly one"},
		{"no manifest", rawZip(t, hdr(PluginWasm, "x")), "exactly one"},
		{"plugin without wasm", rawZip(t, good), "needs plugin.wasm"},
		{"theme with wasm", rawZip(t, hdr(ThemeManifest, themeJSON), hdr(PluginWasm, "x")), "cannot hold"},
		{"bad plugin manifest", rawZip(t, hdr(PluginManifest, `{"name":"X"}`), hdr(PluginWasm, "x")), "invalid plugin name"},
		{"bad theme name", rawZip(t, hdr(ThemeManifest, `{"name":"../x","version":"1.0.0"}`)), "invalid theme name"},
		{"bad theme version", rawZip(t, hdr(ThemeManifest, `{"name":"abc","version":"one"}`)), "semver"},
		{"not a zip", []byte("PK nope"), "not a valid zip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { mustRefuse(t, tt.raw, tt.want) })
	}
}

func TestSymlinkIsRefused(t *testing.T) {
	link := &zip.FileHeader{Name: "assets/link", Method: zip.Store, Comment: "/etc/passwd"}
	link.SetMode(0o777 | fs.ModeSymlink)
	mustRefuse(t, rawZip(t, hdr(PluginManifest, pluginJSON), hdr(PluginWasm, "x"), link), "not a regular file")
}

func TestZipBombIsRefused(t *testing.T) {
	bomb := strings.Repeat("\x00", 8<<20) // 8 MiB of zeros deflates to ~8 KiB
	raw := rawZip(t, hdr(PluginManifest, pluginJSON), hdr(PluginWasm, "x"), hdr("assets/bomb.bin", bomb))
	if len(raw) > 1<<20 {
		t.Fatalf("test archive is %d bytes; not a bomb", len(raw))
	}
	mustRefuse(t, raw, "zip bomb")
}

func TestOversizedIsRefused(t *testing.T) {
	small := Limits{Archive: 1 << 20, Entries: 4, File: 1 << 10, Total: 2 << 10, Ratio: 1000, RatioFloor: 1 << 30, Path: 64}
	for name, raw := range map[string][]byte{
		"file": rawZip(t, hdr(PluginManifest, pluginJSON), hdr(PluginWasm, strings.Repeat("a", 2<<10))),
		"total": rawZip(t, hdr(PluginManifest, pluginJSON), hdr(PluginWasm, "x"),
			hdr("assets/a", strings.Repeat("a", 900)), hdr("assets/b", strings.Repeat("b", 900)), hdr("assets/c", strings.Repeat("c", 900))),
		"archive": bytes.Repeat([]byte("z"), 2<<20),
		"entries": rawZip(t, hdr(PluginManifest, pluginJSON), hdr(PluginWasm, "x"), hdr("assets/1", "1"), hdr("assets/2", "2"), hdr("assets/3", "3")),
		"path":    rawZip(t, hdr(PluginManifest, pluginJSON), hdr(PluginWasm, "x"), hdr("assets/"+strings.Repeat("n", 80), "1")),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(raw, small); err == nil {
				t.Fatal("expected a refusal")
			}
		})
	}
}

func TestDeclaredSizeIsNotTrusted(t *testing.T) {
	// The reader reports what the header says; Parse reads what is there. A
	// header claiming a tiny size over a big body must not slip past the caps.
	raw := rawZip(t, hdr(PluginManifest, pluginJSON), hdr(PluginWasm, "x"), hdr("assets/big", strings.Repeat("ab", 600<<10)))
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil || len(zr.File) != 3 {
		t.Fatalf("setup: %v", err)
	}
	lim := DefaultLimits
	lim.File = 1 << 20 // 1.2 MB body, 1 MiB cap: refused on the real bytes too
	if _, err := Parse(raw, lim); err == nil {
		t.Fatal("expected a refusal")
	}
}

func TestBytesOutsideTheDigestAreRefused(t *testing.T) {
	raw, err := PackFiles(pluginFiles(), nil)
	if err != nil {
		t.Fatal(err)
	}
	mustRefuse(t, append([]byte("junk"), raw...), "must start with its first entry")
	// An archive comment sits after the end-of-central-directory record's fixed
	// part: set its length and append the comment.
	withComment := append(bytes.Clone(raw), []byte("hi")...)
	withComment[len(raw)-2] = 2
	mustRefuse(t, withComment, "comment")
}

func TestEntryCountIsCheckedBeforeTheDirectoryIsRead(t *testing.T) {
	small := DefaultLimits
	small.Entries = 3
	raw := rawZip(t, hdr(PluginManifest, pluginJSON), hdr(PluginWasm, "x"), hdr("assets/1", "1"), hdr("assets/2", "2"))
	err := checkEnvelope(raw, small)
	if err == nil || !strings.Contains(err.Error(), "more than 3 entries") {
		t.Fatalf("err = %v", err)
	}
}
