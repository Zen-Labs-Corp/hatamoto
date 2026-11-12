package backup

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/portability"
	"hatamoto.dev/core/internal/s3/s3test"
)

// referencing stubs the export so it reports paths as referenced, the way the
// real export does in both storage modes.
func referencing(r *rig, paths ...string) {
	r.e.exportFn = func(_ context.Context, w io.Writer, o portability.ExportOptions) (*portability.Manifest, error) {
		for _, p := range paths {
			o.OnMedia(p)
		}
		_, err := w.Write([]byte("content"))
		return &portability.Manifest{Rows: map[string]int64{"hatamoto_media": int64(len(paths))}}, err
	}
}

func logTo(r *rig) *bytes.Buffer {
	var buf bytes.Buffer
	r.e.opts.Log = slog.New(slog.NewTextHandler(&buf, nil))
	return &buf
}

func writeMediaFile(t *testing.T, dir, key string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCreateWarnsAboutRowlessLocalFiles(t *testing.T) {
	r := newRig(t)
	log := logTo(r)
	dir := r.e.mediaDir()
	for _, k := range []string{"tenant/known.png", "tenant/loose.png", ".import-1/b.png"} {
		writeMediaFile(t, dir, k)
	}
	referencing(r, "tenant/known.png")
	info, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err != nil || info.State != StateOK {
		t.Fatalf("info = %+v err = %v", info, err)
	}
	if info.MediaRowless != 1 || len(info.MediaRowlessSample) != 1 || info.MediaRowlessSample[0] != "tenant/loose.png" {
		t.Errorf("rowless = %d %v, want 1 [tenant/loose.png]", info.MediaRowless, info.MediaRowlessSample)
	}
	if got := log.String(); !strings.Contains(got, "no media row") || !strings.Contains(got, "tenant/loose.png") {
		t.Errorf("log = %q, want the warning with the key", got)
	}
	rec, err := r.e.Get(info.ID)
	if err != nil || rec.MediaRowless != 1 {
		t.Errorf("sidecar = %+v, %v", rec, err)
	}
}

func TestCreateSilentOnceRowlessFilesHaveRows(t *testing.T) {
	r := newRig(t)
	log := logTo(r)
	writeMediaFile(t, r.e.mediaDir(), "tenant/a.png")
	writeMediaFile(t, r.e.mediaDir(), "tenant/a_thumb.png")
	referencing(r, "tenant/a.png", "tenant/a_thumb.png")
	info, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err != nil || info.MediaRowless != 0 || info.MediaRowlessSample != nil || strings.Contains(log.String(), "no media row") {
		t.Errorf("info = %+v err = %v log = %q, want no warning", info, err, log.String())
	}
}

func TestCreateRowlessWithoutMediaDirIsSilent(t *testing.T) {
	r := newRig(t)
	referencing(r)
	info, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err != nil || info.MediaRowless != 0 {
		t.Fatalf("info = %+v err = %v", info, err)
	}
	if _, err := os.Stat(r.e.mediaDir()); err == nil {
		t.Error("the check created the media directory")
	}
}

func TestCreateWarnsAboutRowlessS3Objects(t *testing.T) {
	fake := s3test.NewServer(t)
	r, _ := s3Rig(t, fake, "tenant/known.png")
	log := logTo(r)
	store := r.e.opts.MediaStore
	for _, k := range []string{"tenant/known.png", "tenant/loose.png"} {
		if _, err := store.Put(t.Context(), k, strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}
	info, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err != nil || info.State != StateOK {
		t.Fatalf("info = %+v err = %v", info, err)
	}
	if info.MediaRowless != 1 || len(info.MediaRowlessSample) != 1 || info.MediaRowlessSample[0] != "tenant/loose.png" {
		t.Errorf("rowless = %d %v, want 1 [tenant/loose.png]", info.MediaRowless, info.MediaRowlessSample)
	}
	if !strings.Contains(log.String(), "tenant/loose.png") {
		t.Errorf("log = %q", log.String())
	}
}
