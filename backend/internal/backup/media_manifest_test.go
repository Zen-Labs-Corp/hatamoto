package backup

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/portability"
	"hatamoto.dev/core/internal/s3/s3test"
)

func newMediaStore(t *testing.T, fake *s3test.Server) *media.S3Store {
	t.Helper()
	st, err := media.NewS3Store(media.S3Target{Endpoint: fake.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		Prefix: "site/", AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// s3Rig is a backup rig whose media lives in a fake bucket. The stubbed export
// reports the referenced paths the way the real one does and records the
// options it got.
func s3Rig(t *testing.T, fake *s3test.Server, paths ...string) (*rig, *portability.ExportOptions) {
	t.Helper()
	r := newRig(t)
	r.e.opts.MediaStore = newMediaStore(t, fake)
	var got portability.ExportOptions
	r.e.exportFn = func(_ context.Context, w io.Writer, o portability.ExportOptions) (*portability.Manifest, error) {
		got = o
		man := &portability.Manifest{Rows: map[string]int64{"hatamoto_media": int64(len(paths))}}
		for _, p := range paths {
			if o.OnMedia != nil {
				o.OnMedia(p)
			}
		}
		_, err := w.Write([]byte("content"))
		return man, err
	}
	return r, &got
}

func archiveEntry(t *testing.T, path, name string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err != nil {
			t.Fatalf("%s not in %s", name, path)
		}
		if h.Name == name {
			b, _ := io.ReadAll(tr)
			return b
		}
	}
}

func TestBackupOnObjectStorageWritesMediaManifest(t *testing.T) {
	ctx := context.Background()
	fake := s3test.NewServer(t)
	r, got := s3Rig(t, fake, "2026/a.png", "2026/a-medium.webp", "2026/gone.png")
	store := r.e.opts.MediaStore
	body := "bytes of a"
	if _, err := store.Put(ctx, "2026/a.png", strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(ctx, "2026/a-medium.webp", strings.NewReader("m")); err != nil {
		t.Fatal(err)
	}
	// An object written elsewhere has no checksum metadata: it is hashed by reading it.
	fake.Put("site/2026/other.png", []byte("x"), nil)
	r.e.exportFn = wrapPaths(r.e.exportFn, "2026/other.png")

	info, err := r.e.Create(ctx, Request{Kind: KindManual})
	if err != nil {
		t.Fatal(err)
	}
	if !got.SkipMedia || got.Store == nil || got.OnMedia == nil {
		t.Errorf("export options = %+v", got)
	}
	arch := filepath.Join(r.data, "backups", info.ID+".tar")
	names := tarNames(t, arch)
	for _, n := range names {
		if strings.HasPrefix(n, "media/") {
			t.Errorf("unexpected media entry %s", n)
		}
	}
	if len(names) < 2 || names[len(names)-1] != "manifest.json" || names[len(names)-2] != mediaManifestName {
		t.Fatalf("entries = %v", names)
	}

	var mm mediaManifest
	if err := jsonx.Unmarshal(archiveEntry(t, arch, mediaManifestName), &mm, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	want := map[string]mediaManifestFile{
		"2026/a.png":         {Key: "2026/a.png", Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])},
		"2026/a-medium.webp": {Key: "2026/a-medium.webp", Size: 1, SHA256: hex.EncodeToString(sha256sum("m"))},
		"2026/other.png":     {Key: "2026/other.png", Size: 1, SHA256: hex.EncodeToString(sha256sum("x"))},
	}
	if len(mm.Files) != len(want) || mm.Version != mediaManifestVersion {
		t.Fatalf("manifest = %+v", mm)
	}
	for _, f := range mm.Files {
		if want[f.Key] != f {
			t.Errorf("entry %+v, want %+v", f, want[f.Key])
		}
	}
	var mf manifest
	if err := jsonx.Unmarshal(archiveEntry(t, arch, manifestName), &mf, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	if mf.MediaStorage != "s3" || mf.MediaMissing != 1 {
		t.Errorf("manifest media = %q missing %d", mf.MediaStorage, mf.MediaMissing)
	}
	if strings.Contains(string(archiveEntry(t, arch, manifestName)), s3test.SecretKey) {
		t.Error("the secret key is in the manifest")
	}
}

func sha256sum(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

// wrapPaths makes the stubbed export also report extra paths.
func wrapPaths(f exportFunc, extra ...string) exportFunc {
	return func(ctx context.Context, w io.Writer, o portability.ExportOptions) (*portability.Manifest, error) {
		for _, p := range extra {
			o.OnMedia(p)
		}
		return f(ctx, w, o)
	}
}

func TestBackupWithMediaOnObjectStorageKeepsBytes(t *testing.T) {
	fake := s3test.NewServer(t)
	r, got := s3Rig(t, fake)
	if _, err := r.e.Create(context.Background(), Request{Kind: KindManual, WithMedia: true}); err != nil {
		t.Fatal(err)
	}
	if got.SkipMedia || got.Store == nil {
		t.Errorf("export options = %+v", got)
	}
}

// Local media: no manifest member, no store handed to the export.
func TestLocalBackupHasNoMediaManifest(t *testing.T) {
	r := newRig(t)
	var got portability.ExportOptions
	inner := r.e.exportFn
	r.e.exportFn = func(ctx context.Context, w io.Writer, o portability.ExportOptions) (*portability.Manifest, error) {
		got = o
		return inner(ctx, w, o)
	}
	info, err := r.e.Create(context.Background(), Request{Kind: KindManual, WithMedia: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range tarNames(t, filepath.Join(r.data, "backups", info.ID+".tar")) {
		if n == mediaManifestName {
			t.Error("a local backup carries a media manifest")
		}
	}
	if got.Store != nil || got.SkipMedia || got.OnMedia != nil {
		t.Errorf("export options = %+v", got)
	}
}

// An unreachable bucket fails the backup with a clear error, not a hang.
func TestBackupOnObjectStorageFailsWhenBucketDown(t *testing.T) {
	fake := s3test.NewServer(t)
	r, _ := s3Rig(t, fake, "a.png")
	dead := httptest.NewServer(nil)
	endpoint := dead.URL
	dead.Close()
	store, err := media.NewS3Store(media.S3Target{Endpoint: endpoint, Region: s3test.Region, Bucket: s3test.Bucket,
		AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.e.opts.MediaStore = store
	if _, err := r.e.Create(context.Background(), Request{Kind: KindManual}); err == nil || !strings.Contains(err.Error(), "media manifest") {
		t.Fatalf("err = %v", err)
	}
}

// Every referenced object missing means a wrong bucket or prefix: the backup
// fails instead of recording an empty manifest.
func TestBackupOnObjectStorageFailsWhenNoReferencedObjectExists(t *testing.T) {
	fake := s3test.NewServer(t)
	r, _ := s3Rig(t, fake, "2026/gone.png", "2026/gone-medium.webp")
	if _, err := r.e.Create(context.Background(), Request{Kind: KindManual}); err == nil || !strings.Contains(err.Error(), "none of the 2 media objects") {
		t.Fatalf("err = %v", err)
	}
}
