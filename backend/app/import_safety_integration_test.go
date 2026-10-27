//go:build integration

package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/portability"
)

type tarEntry struct{ name, body string }

// craft builds an archive by hand, so the tests can say what a hostile or
// damaged one looks like.
func craft(t *testing.T, header string, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	all := append([]tarEntry{{"format.json", header}}, entries...)
	for _, e := range all {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func migratedScratch(t *testing.T) (context.Context, func(archive []byte, media string) (*portability.ImportResult, error), func() int) {
	t.Helper()
	ctx := context.Background()
	_, pool := scratchDB(t)
	if err := migrateUp(ctx, pool, quietLogger()); err != nil {
		t.Fatal(err)
	}
	imp := func(a []byte, media string) (*portability.ImportResult, error) {
		return portability.Import(ctx, pool, bytes.NewReader(a), portability.ImportOptions{MediaDir: media})
	}
	rows := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_media`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	return ctx, imp, rows
}

const (
	mediaHeader = `{"format":"hatamoto-export","version":1,"schema_version":0,"tables":["hatamoto_media"]}`
	emptyHeader = `{"format":"hatamoto-export","version":1,"schema_version":0,"tables":[]}`
)

func mediaRow(path string) string {
	return `{"id":"11111111-1111-1111-1111-111111111111","filename":"x","original_filename":"x","mime_type":"image/png","file_size":1,"storage_path":"` + path + `"}` + "\n"
}

// The reviewer's payload: a media row pointing outside the upload directory.
func TestImportRejectsTraversingMediaRow_Integration(t *testing.T) {
	_, imp, rows := migratedScratch(t)
	media := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	_ = os.WriteFile(victim, []byte("precious"), 0o644)
	for _, p := range []string{"../../../../tmp/hm34x/victim", "/etc/passwd", "a/../../b"} {
		a := craft(t, mediaHeader,
			tarEntry{"data/hatamoto_media.jsonl", mediaRow(p)},
			tarEntry{"manifest.json", `{"rows":{"hatamoto_media":1},"media_files":0,"media_bytes":0}`})
		if _, err := imp(a, media); err == nil || !strings.Contains(err.Error(), "unsafe media path") {
			t.Errorf("storage_path %q: err = %v", p, err)
		}
	}
	if n := rows(); n != 0 {
		t.Errorf("%d media rows imported from hostile archives", n)
	}
}

// A failed import must leave files that were already in the media dir alone.
func TestFailedImportKeepsExistingMedia_Integration(t *testing.T) {
	_, imp, _ := migratedScratch(t)
	media := t.TempDir()
	keep := filepath.Join(media, "keep.png")
	_ = os.WriteFile(keep, []byte("original"), 0o644)

	// Same name as an existing file: refused, nothing overwritten or deleted.
	a := craft(t, emptyHeader, tarEntry{"media/keep.png", "\x89PNG\r\n\x1a\nfrom archive"},
		tarEntry{"manifest.json", `{"rows":{},"media_files":1,"media_bytes":12}`})
	if _, err := imp(a, media); err == nil {
		t.Fatal("import over an existing media file succeeded")
	}
	// A new file plus a bad manifest: the new file is not left behind either.
	a = craft(t, emptyHeader, tarEntry{"media/new.png", "\x89PNG\r\n\x1a\nnew"},
		tarEntry{"manifest.json", `{"rows":{},"media_files":5,"media_bytes":3}`})
	if _, err := imp(a, media); err == nil {
		t.Fatal("bad manifest accepted")
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "original" {
		t.Errorf("keep.png = %q, %v", b, err)
	}
	entries, _ := os.ReadDir(media)
	if len(entries) != 1 {
		t.Errorf("media dir holds %d entries after failed imports, want only keep.png", len(entries))
	}
}

func TestImportRefusesNewerSchema_Integration(t *testing.T) {
	_, imp, _ := migratedScratch(t)
	a := craft(t, `{"format":"hatamoto-export","version":1,"schema_version":99999999999999,"tables":[]}`,
		tarEntry{"manifest.json", `{"rows":{},"media_files":0,"media_bytes":0}`})
	if _, err := imp(a, t.TempDir()); err == nil || !strings.Contains(err.Error(), "newer Hatamoto") {
		t.Errorf("err = %v", err)
	}
}

// Files that older versions stored without sniffing (an .svg, a .txt) are not
// served as media any more. They must not stop the import: the file is skipped
// with a warning and everything else restores.
func TestImportSkipsLegacyDisallowedMedia_Integration(t *testing.T) {
	_, imp, rows := migratedScratch(t)
	media := t.TempDir()
	row := func(id, path string) string {
		return `{"id":"` + id + `","filename":"x","original_filename":"x","mime_type":"image/png","file_size":1,"storage_path":"` + path + `"}` + "\n"
	}
	a := craft(t, mediaHeader,
		tarEntry{"data/hatamoto_media.jsonl", row("11111111-1111-1111-1111-111111111111", "old/logo.svg") +
			row("22222222-2222-2222-2222-222222222222", "old/notes.txt") +
			row("33333333-3333-3333-3333-333333333333", "old/ok.png")},
		tarEntry{"media/old/logo.svg", "<svg xmlns='http://www.w3.org/2000/svg'><script/></svg>"},
		tarEntry{"media/old/notes.txt", "plain notes"},
		tarEntry{"media/old/ok.png", "\x89PNG\r\n\x1a\nok"},
		tarEntry{"manifest.json", `{"rows":{"hatamoto_media":3},"media_files":3,"media_bytes":` +
			strconv.Itoa(len("<svg xmlns='http://www.w3.org/2000/svg'><script/></svg>")+len("plain notes")+len("\x89PNG\r\n\x1a\nok")) + `}`})
	res, err := imp(a, media)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(res.Warnings) != 2 {
		t.Errorf("warnings = %v, want one per skipped file", res.Warnings)
	}
	for _, w := range res.Warnings {
		if !strings.Contains(w, "logo.svg") && !strings.Contains(w, "notes.txt") {
			t.Errorf("unexpected warning %q", w)
		}
	}
	if rows() != 3 {
		t.Errorf("media rows = %d, want 3", rows())
	}
	for name, want := range map[string]bool{"old/logo.svg": false, "old/notes.txt": false, "old/ok.png": true} {
		if _, err := os.Stat(filepath.Join(media, name)); (err == nil) != want {
			t.Errorf("%s present=%v, want %v", name, err == nil, want)
		}
	}
}
