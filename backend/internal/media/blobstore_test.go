package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func newTestStore(t *testing.T) (BlobStore, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewLocalStore(dir, "/media")
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestLocalStorePutGetRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	data := bytes.Repeat([]byte("abc"), 10000)
	n, err := s.Put(ctx, "a/b/file.bin", bytes.NewReader(data))
	if err != nil || n != int64(len(data)) {
		t.Fatalf("Put = %d, %v", n, err)
	}
	rc, err := s.Get(ctx, "a/b/file.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, data) {
		t.Fatal("round trip bytes differ")
	}
}

func TestLocalStoreMissingIsNotExist(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Get(ctx, "nope/x"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Get missing = %v", err)
	}
	if err := s.Delete(ctx, "nope/x"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Delete missing = %v", err)
	}
	if _, err := s.Put(ctx, "k/x", strings.NewReader("1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "k/x"); err != nil {
		t.Errorf("Delete existing = %v", err)
	}
}

func TestLocalStorePutRefusesEscapes(t *testing.T) {
	s, dir := newTestStore(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"../x", "a/../../x", "/abs/x", "", "link/x"} {
		if _, err := s.Put(context.Background(), key, strings.NewReader("data")); err == nil {
			t.Errorf("Put(%q) succeeded", key)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("wrote outside root: %v", entries)
	}
}

func TestLocalStorePutFailureLeavesNoFile(t *testing.T) {
	s, dir := newTestStore(t)
	orig := createUpload
	createUpload = func(p string) (io.WriteCloser, error) {
		f, err := os.Create(p)
		if err != nil {
			return nil, err
		}
		return &closeFails{f}, nil
	}
	t.Cleanup(func() { createUpload = orig })

	if _, err := s.Put(context.Background(), "d/f.bin", strings.NewReader("data")); err == nil {
		t.Fatal("expected error")
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "d", "*")); len(m) != 0 {
		t.Errorf("partial file left: %v", m)
	}
}

type closeFails struct{ f *os.File }

func (c *closeFails) Write(p []byte) (int, error) { return c.f.Write(p) }
func (c *closeFails) Close() error                { c.f.Close(); return errors.New("close failed") }

func TestLocalStorePermissions(t *testing.T) {
	old := syscall.Umask(022)
	t.Cleanup(func() { syscall.Umask(old) })
	s, dir := newTestStore(t)
	if _, err := s.Put(context.Background(), "d/f.bin", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	di, _ := os.Stat(filepath.Join(dir, "d"))
	fi, _ := os.Stat(filepath.Join(dir, "d", "f.bin"))
	if di.Mode().Perm() != 0755 || fi.Mode().Perm() != 0644 {
		t.Errorf("modes dir %v file %v", di.Mode().Perm(), fi.Mode().Perm())
	}
}

func TestLocalStorePutRefusesSymlinkedParentAndFile(t *testing.T) {
	s, dir := newTestStore(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "target"), filepath.Join(dir, "f")); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"link/sub/x", "link/x", "f"} {
		if _, err := s.Put(context.Background(), key, strings.NewReader("data")); err == nil {
			t.Errorf("Put(%q) succeeded", key)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("wrote outside root: %v", entries)
	}
}
