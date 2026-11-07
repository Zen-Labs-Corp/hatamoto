package form

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestStore(t *testing.T) (*FileStore, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "private", "form-uploads")
	s, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return s, root
}

func countFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func TestFileStoreLifecycleAndModes(t *testing.T) {
	t.Parallel()
	s, root := newTestStore(t)
	form := uuid.New()
	st, err := s.Stage(strings.NewReader("%PDF-1.4 hello"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size != 14 {
		t.Fatalf("size = %d", st.Size)
	}
	if typ, err := st.Check("pdf"); err != nil || typ != "application/pdf" {
		t.Fatalf("check = %q, %v", typ, err)
	}
	id, err := s.Commit(form, st)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidFileID(id) {
		t.Fatalf("id %q not generated-shaped", id)
	}
	st.Discard() // after commit: no-op, must not remove the stored file
	f, err := s.Open(form, id)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if countFiles(t, filepath.Join(root, tmpDir)) != 0 {
		t.Error("tmp not empty after commit")
	}
	if runtime.GOOS != "windows" {
		for p, want := range map[string]os.FileMode{
			root: 0o700, filepath.Join(root, form.String()): 0o700, filepath.Join(root, form.String(), id): 0o600,
		} {
			fi, err := os.Stat(p)
			if err != nil || fi.Mode().Perm() != want {
				t.Errorf("%s mode = %v (%v), want %v", p, fi.Mode().Perm(), err, want)
			}
		}
	}
	if err := s.Delete(form, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(form, id); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("open after delete: %v", err)
	}
	if err := s.Delete(form, id); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}

func TestFileStoreStageLimitAndDiscard(t *testing.T) {
	t.Parallel()
	s, root := newTestStore(t)
	if _, err := s.Stage(strings.NewReader(strings.Repeat("x", 11)), 10); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("over limit: %v", err)
	}
	if _, err := s.Stage(strings.NewReader(strings.Repeat("x", 10)), 10); err != nil {
		t.Fatalf("at limit: %v", err)
	}
	st, err := s.Stage(strings.NewReader("abc"), 10)
	if err != nil {
		t.Fatal(err)
	}
	st.Discard()
	st.Discard()
	// one staged file (the at-limit one) remains; the rejected and discarded ones are gone
	if n := countFiles(t, root); n != 1 {
		t.Fatalf("files left = %d, want 1", n)
	}
	if _, err := s.Commit(uuid.New(), st); err == nil {
		t.Fatal("commit of a discarded upload succeeded")
	}
}

func TestFileStoreIDsAreNeverPaths(t *testing.T) {
	t.Parallel()
	s, root := newTestStore(t)
	form := uuid.New()
	outside := filepath.Join(filepath.Dir(root), "secret")
	if err := os.WriteFile(outside, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../../secret", "..", "", "a/b", `a\b`, strings.Repeat("A", 32), strings.Repeat("a", 31), strings.Repeat("a", 33), "../" + strings.Repeat("a", 29)} {
		if _, err := s.Open(form, id); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Open(%q) = %v, want ErrNotExist", id, err)
		}
		if err := s.Delete(form, id); err != nil {
			t.Errorf("Delete(%q) = %v", id, err)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("file outside the store was touched: %v", err)
	}
}

func TestFileStoreSymlinkEscapeRefused(t *testing.T) {
	t.Parallel()
	s, root := newTestStore(t)
	form := uuid.New()
	outsideDir := t.TempDir()
	secret := filepath.Join(outsideDir, strings.Repeat("a", 32))
	if err := os.WriteFile(secret, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(root, form.String())); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if f, err := s.Open(form, strings.Repeat("a", 32)); err == nil {
		f.Close()
		t.Fatal("Open followed a symlink out of the store")
	}
	if err := s.Delete(form, strings.Repeat("a", 32)); err == nil {
		t.Error("Delete followed a symlink out of the store")
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("outside file was removed: %v", err)
	}
	st, err := s.Stage(strings.NewReader("x"), 10)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Discard()
	if _, err := s.Commit(form, st); err == nil {
		t.Fatal("Commit wrote through a symlinked form directory")
	}
	if entries, _ := os.ReadDir(outsideDir); len(entries) != 1 {
		t.Fatalf("outside dir gained files: %v", entries)
	}
}

func TestFileStoreDeleteForm(t *testing.T) {
	t.Parallel()
	s, root := newTestStore(t)
	a, b := uuid.New(), uuid.New()
	commit := func(form uuid.UUID) string {
		st, err := s.Stage(strings.NewReader("x"), 10)
		if err != nil {
			t.Fatal(err)
		}
		id, err := s.Commit(form, st)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	commit(a)
	commit(a)
	keep := commit(b)
	if err := s.DeleteForm(a); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteForm(a); err != nil {
		t.Fatalf("second DeleteForm: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, a.String())); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("form dir still there: %v", err)
	}
	f, err := s.Open(b, keep)
	if err != nil {
		t.Fatalf("other form's file lost: %v", err)
	}
	f.Close()
}

func TestSanitizeName(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("é", 400) + ".pdf"
	tests := []struct{ in, want string }{
		{"report.pdf", "report.pdf"},
		{"../x", "x"},
		{"../../etc/passwd", "passwd"},
		{`a/b\c`, "c"},
		{`C:\Users\me\cv.docx`, "cv.docx"},
		{"a\x00b\r\nc\x1b[0m.pdf", "abc[0m.pdf"},
		{"inv\u202eoice.pdf", "invoice.pdf"},
		{"..", "file"},
		{"   ", "file"},
		{"", "file"},
		{"/", "file"},
		{".hidden", "hidden"},
		{"bad\xffutf8.txt", "badutf8.txt"},
		{"résumé.pdf", "résumé.pdf"},
	}
	for _, tc := range tests {
		if got := SanitizeName(tc.in); got != tc.want {
			t.Errorf("SanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	got := SanitizeName(long)
	if n := len([]rune(got)); n != maxFileNameRunes || !strings.HasSuffix(got, ".pdf") {
		t.Errorf("long name: %d runes, suffix ok=%v", n, strings.HasSuffix(got, ".pdf"))
	}
	if g := SanitizeName(strings.Repeat("a", 500)); len([]rune(g)) != maxFileNameRunes {
		t.Errorf("no-ext long name: %d runes", len([]rune(g)))
	}
}

// Staged files a dead process left behind are swept when the store starts;
// a recent one (an upload in progress) is kept.
func TestNewFileStoreSweepsStaleStagedFiles(t *testing.T) {
	t.Parallel()
	_, root := newTestStore(t)
	for _, n := range []string{"stale", "fresh"} {
		if err := os.MkdirAll(filepath.Join(root, tmpDir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, tmpDir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * stagedMaxAge)
	if err := os.Chtimes(filepath.Join(root, tmpDir, "stale"), old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, tmpDir, "stale")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stale staged file kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, tmpDir, "fresh")); err != nil {
		t.Errorf("recent staged file removed: %v", err)
	}
}
