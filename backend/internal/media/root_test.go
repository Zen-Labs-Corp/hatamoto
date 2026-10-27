package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

type deleteRepo struct {
	fakeRepo
	m *Media
}

func (r *deleteRepo) GetByID(context.Context, uuid.UUID) (*Media, error) { return r.m, nil }

func TestOpenFile_RefusesTraversalAndSymlinkEscape(t *testing.T) {
	svc := newTestService(t, Config{})
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.png")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(svc.config.UploadDir, "ok.png")
	if err := os.WriteFile(inside, []byte("fine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(svc.config.UploadDir, "link.png")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := os.Symlink(outside, filepath.Join(svc.config.UploadDir, "dir")); err != nil {
		t.Fatal(err)
	}

	f, err := svc.OpenFile("ok.png")
	if err != nil {
		t.Fatalf("regular file refused: %v", err)
	}
	if b, _ := io.ReadAll(f); string(b) != "fine" {
		t.Errorf("read %q", b)
	}
	f.Close()

	for _, rel := range []string{"../" + filepath.Base(outside) + "/secret.png", "link.png", "dir/secret.png", secret} {
		if f, err := svc.OpenFile(rel); err == nil {
			f.Close()
			t.Errorf("OpenFile(%q) escaped the upload directory", rel)
		}
	}
}

func TestDelete_StaysInsideUploadDir(t *testing.T) {
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.png")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := newTestService(t, Config{})
	own := filepath.Join(svc.config.UploadDir, "own.png")
	if err := os.WriteFile(own, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(svc.config.UploadDir, "dir")); err != nil {
		t.Skip("symlinks unavailable")
	}
	svc.repo = &deleteRepo{m: &Media{
		StoragePath: "own.png",
		Variants: []ImageVariant{
			{Path: "../" + filepath.Base(outside) + "/victim.png"},
			{Path: "dir/victim.png"},
			{Path: victim},
		},
	}}
	if err := svc.Delete(context.Background(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("a file outside the upload directory was deleted: %v", err)
	}
	if _, err := os.Stat(own); !os.IsNotExist(err) {
		t.Error("the media file itself was not deleted")
	}
}

// closeFailWriter writes fine but fails on Close, like a full disk flushing.
type closeFailWriter struct{ io.WriteCloser }

func (w closeFailWriter) Close() error { w.WriteCloser.Close(); return errors.New("close failed") }

func TestUpload_FailedCloseRemovesTheFile(t *testing.T) {
	svc := newTestService(t, Config{})
	orig := createUpload
	createUpload = func(p string) (io.WriteCloser, error) {
		f, err := orig(p)
		return closeFailWriter{f}, err
	}
	t.Cleanup(func() { createUpload = orig })

	_, err := svc.Upload(context.Background(), nil, bytes.NewReader(pngBytes(t, 8, 8)), "a.png", "image/png", 100)
	if !errors.Is(err, ErrStorageFailed) {
		t.Fatalf("err = %v, want ErrStorageFailed", err)
	}
	left, _ := filepath.Glob(filepath.Join(svc.config.UploadDir, "*", "*"))
	if len(left) != 0 {
		t.Errorf("files left behind: %v", left)
	}
}

// A symlinked directory inside the upload directory is refused at upload,
// with an error, rather than succeeding and then failing every read.
func TestUpload_SymlinkedDirInsideUploadDirFailsLoudly(t *testing.T) {
	svc := newTestService(t, Config{})
	other := t.TempDir()
	if err := os.Symlink(other, filepath.Join(svc.config.UploadDir, legacyMediaTenantDir)); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Upload(context.Background(), nil, bytes.NewReader(pngBytes(t, 8, 8)), "a.png", "image/png", 100)
	if !errors.Is(err, ErrStorageFailed) {
		t.Fatalf("err = %v, want ErrStorageFailed", err)
	}
	if left, _ := filepath.Glob(filepath.Join(other, "*")); len(left) != 0 {
		t.Errorf("file written through the symlink: %v", left)
	}
}

// A symlinked upload directory itself works: it is resolved once at start-up,
// and uploads, variants and reads all go through the real path.
func TestUpload_SymlinkedUploadRootWorks(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "uploads")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	svc := NewService(&fakeRepo{}, Config{UploadDir: link, BaseURL: "/media", MaxSizeMB: 10, MaxPixels: defaultMaxPixels}, slog.Default())
	m, err := svc.Upload(context.Background(), nil, bytes.NewReader(pngBytes(t, 400, 300)), "a.png", "image/png", 100)
	if err != nil {
		t.Fatal(err)
	}
	f, err := svc.OpenFile(m.StoragePath)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	f.Close()
	if len(m.Variants) == 0 {
		t.Error("no variants were made under the symlinked root")
	}
	for _, v := range m.Variants {
		if _, err := os.Stat(filepath.Join(real, v.Path)); err != nil {
			t.Errorf("variant %s: %v", v.Path, err)
		}
	}
}
