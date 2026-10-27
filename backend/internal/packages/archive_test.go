package packages

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
)

type zent struct {
	name string
	body string
	mode os.FileMode
}

func buildZip(t *testing.T, ents ...zent) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range ents {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func shaOf(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestReadArchiveGood(t *testing.T) {
	flat := buildZip(t, zent{name: "theme.json", body: "{}"}, zent{name: "assets/", mode: os.ModeDir | 0o755}, zent{name: "assets/a.css", body: "a{}"}, zent{name: "plugin.wasm", body: "\x00asm"})
	wrapped := buildZip(t, zent{name: "repo-1/", mode: os.ModeDir | 0o755}, zent{name: "repo-1/theme.json", body: "{}"}, zent{name: "repo-1/assets/a.css", body: "a{}"})
	for name, tc := range map[string]struct {
		data []byte
		want []string
	}{
		"flat":    {flat, []string{"theme.json", "assets/a.css", "plugin.wasm"}},
		"wrapped": {wrapped, []string{"theme.json", "assets/a.css"}},
	} {
		t.Run(name, func(t *testing.T) {
			files, err := ReadArchive(tc.data, shaOf(tc.data), DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != len(tc.want) {
				t.Fatalf("got %d files, want %v", len(files), tc.want)
			}
			for _, w := range tc.want {
				if _, ok := files[w]; !ok {
					t.Errorf("missing %s", w)
				}
			}
		})
	}
}

func TestReadArchiveTwoTopDirsNotStripped(t *testing.T) {
	data := buildZip(t, zent{name: "a/theme.json", body: "{}"}, zent{name: "b/x.txt", body: "x"})
	files, err := ReadArchive(data, shaOf(data), DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["a/theme.json"]; !ok || len(files) != 2 {
		t.Fatalf("files were rewritten: %v", files)
	}
}

func TestReadArchiveStripsOnlyOneLevel(t *testing.T) {
	data := buildZip(t, zent{name: "w/inner/theme.json", body: "{}"})
	files, err := ReadArchive(data, shaOf(data), DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["inner/theme.json"]; !ok {
		t.Fatalf("got %v", files)
	}
}

func TestReadArchiveRefusals(t *testing.T) {
	good := buildZip(t, zent{name: "theme.json", body: "{}"})
	small := DefaultLimits
	small.Entries = 3
	bomb := DefaultLimits
	bomb.RatioFloor = 1 << 10
	bomb.Ratio = 50
	var many []zent
	for i := range 5 {
		many = append(many, zent{name: fmt.Sprintf("f%d", i), body: "x"})
	}
	tests := []struct {
		name string
		data []byte
		sha  string
		lim  Limits
		want string
	}{
		{"sha mismatch", good, strings.Repeat("0", 64), DefaultLimits, "does not match"},
		{"bad sha string", good, "ABC", DefaultLimits, "64 lowercase hex"},
		{"uppercase sha", good, strings.ToUpper(shaOf(good)), DefaultLimits, "64 lowercase hex"},
		{"traversal", buildZip(t, zent{name: "../x", body: "x"}), "", DefaultLimits, "traversal"},
		{"absolute", buildZip(t, zent{name: "/abs", body: "x"}), "", DefaultLimits, "absolute"},
		{"hidden", buildZip(t, zent{name: ".hidden", body: "x"}), "", DefaultLimits, "hidden"},
		{"backslash", buildZip(t, zent{name: `a\b`, body: "x"}), "", DefaultLimits, "backslash"},
		{"symlink", buildZip(t, zent{name: "theme.json", body: "/etc/passwd", mode: os.ModeSymlink | 0o777}), "", DefaultLimits, "not a regular file"},
		{"duplicate case-insensitive", buildZip(t, zent{name: "a.txt", body: "1"}, zent{name: "A.txt", body: "2"}), "", DefaultLimits, "duplicate"},
		{"entry count", buildZip(t, many...), "", small, "more than 3 entries"},
		{"zip bomb", buildZip(t, zent{name: "big", body: strings.Repeat("a", 1<<20)}), "", bomb, "zip bomb"},
		{"file too big", buildZip(t, zent{name: "big", body: strings.Repeat("a", 100)}), "", Limits{Archive: 1 << 20, Entries: 10, File: 10, Total: 100, Ratio: 200, RatioFloor: 1 << 20, Path: 200}, "larger than"},
		{"total too big", buildZip(t, zent{name: "a", body: strings.Repeat("a", 8)}, zent{name: "b", body: strings.Repeat("b", 8)}), "", Limits{Archive: 1 << 20, Entries: 10, File: 10, Total: 12, Ratio: 200, RatioFloor: 1 << 20, Path: 200}, "expands to more than"},
		{"not a zip", []byte("hello"), "", DefaultLimits, "not a valid zip"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sha := tc.sha
			if sha == "" {
				sha = shaOf(tc.data)
			}
			files, err := ReadArchive(tc.data, sha, tc.lim)
			if err == nil {
				t.Fatalf("accepted: %v", files)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

// An author's signature.json must never survive a re-pack: the pack is signed
// with the packer's key or not at all.
func TestAuthorSignatureNeverSurvivesPack(t *testing.T) {
	author, packer := newKey(t), newKey(t)
	authorSig := []byte(`{"signer":"` + author.ID + `","algorithm":"ed25519","signature":"AAAA"}`)
	data := buildZip(t, zent{name: "theme.json", body: `{"name":"kumo","version":"1.0.0"}`}, zent{name: SignatureFile, body: string(authorSig)})
	files, err := ReadArchive(data, shaOf(data), DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files[SignatureFile]; ok {
		t.Error("ReadArchive kept the root signature.json")
	}
	files[SignatureFile] = authorSig // PackFiles must also defend itself
	signed, err := PackFiles(files, &packer)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(signed, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if p.Signature == nil || p.Signature.Signer != packer.ID {
		t.Errorf("signature = %+v, want signer %s", p.Signature, packer.ID)
	}
	unsigned, err := PackFiles(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p, err = Parse(unsigned, DefaultLimits); err != nil || p.Signature != nil {
		t.Errorf("unsigned pack: err %v, signature %+v", err, p.Signature)
	}
}
