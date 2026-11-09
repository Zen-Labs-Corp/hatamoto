package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/s3/s3test"
)

// manifestSpec returns a mutator: an archive with no media bytes, a media
// manifest listing files, and no plugins.
func manifestSpec(t *testing.T, files ...mediaManifestFile) func(*archSpec) {
	t.Helper()
	body, err := jsonx.Marshal(mediaManifest{Version: mediaManifestVersion, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	return func(s *archSpec) {
		noPlugins(s)
		s.Media = nil
		s.Bodies[mediaManifestName] = body
		s.Parts = append(s.Parts, mediaManifestName)
	}
}

func sumOf(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func seedBucket(t *testing.T, r *restoreRig, fake *s3test.Server, objs map[string]string) {
	t.Helper()
	r.o.MediaStore = newMediaStore(t, fake)
	for k, v := range objs {
		if _, err := r.o.MediaStore.Put(context.Background(), k, strings.NewReader(v)); err != nil {
			t.Fatal(err)
		}
	}
}

func dryPrint(t *testing.T, r *restoreRig, src string) string {
	t.Helper()
	plan, err := r.dry(t, src)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	plan.Print(&out)
	return out.String()
}

func TestRestoreChecksMediaManifestAgainstBucket(t *testing.T) {
	files := []mediaManifestFile{
		{Key: "a.png", Size: 1, SHA256: sumOf("a")},
		{Key: "b.png", Size: 1, SHA256: sumOf("b")},
		{Key: "c.png", Size: 1, SHA256: sumOf("c")},
	}
	t.Run("all present", func(t *testing.T) {
		fake := s3test.NewServer(t)
		r := newRestoreRig(t)
		seedBucket(t, r, fake, map[string]string{"a.png": "a", "b.png": "b", "c.png": "c"})
		out := dryPrint(t, r, buildArchive(t, manifestSpec(t, files...)))
		if !strings.Contains(out, "media objects ok (3)") || strings.Contains(out, "media objects missing") || !strings.Contains(out, "check 3 media objects in object storage") {
			t.Errorf("output:\n%s", out)
		}
	})
	t.Run("missing and truncated are reported", func(t *testing.T) {
		fake := s3test.NewServer(t)
		r := newRestoreRig(t)
		seedBucket(t, r, fake, map[string]string{"a.png": "a", "b.png": "b"}) // c.png is gone
		fake.Truncate("site/b.png")                                           // size differs; the metadata checksum is trusted when the size agrees
		out := dryPrint(t, r, buildArchive(t, manifestSpec(t, files...)))
		for _, want := range []string{"1 of 3 media objects missing from object storage: c.png", "1 of 3 media objects differ from the backup (size or sha256): b.png"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
	})
	t.Run("bucket without sha256 metadata is hashed", func(t *testing.T) {
		fake := s3test.NewServer(t)
		r := newRestoreRig(t)
		seedBucket(t, r, fake, nil)
		fake.Put("site/a.png", []byte("a"), nil)
		fake.Put("site/b.png", []byte("x"), nil) // same size, other bytes
		out := dryPrint(t, r, buildArchive(t, manifestSpec(t, files[:2]...)))
		if !strings.Contains(out, "1 of 2 media objects differ") || !strings.Contains(out, "b.png") || strings.Contains(out, "a.png") {
			t.Errorf("output:\n%s", out)
		}
	})
	t.Run("no object storage here", func(t *testing.T) {
		r := newRestoreRig(t)
		out := dryPrint(t, r, buildArchive(t, manifestSpec(t, files...)))
		if !strings.Contains(out, "lists 3 media objects in object storage") {
			t.Errorf("output:\n%s", out)
		}
	})
}

// An archive that carries the bytes restores them into the bucket and leaves the
// local media dir alone; a bucket that already has objects is refused.
func TestRestoreWithMediaBytesGoesToBucket(t *testing.T) {
	fake := s3test.NewServer(t)
	var log []string
	r := execRig(t, &log, nil)
	r.o.MediaStore = newMediaStore(t, fake)
	if _, err := realRun(t, r, buildArchive(t, noPlugins)); err != nil {
		t.Fatal(err)
	}
	if got := fake.Objects(); len(got) != 2 || string(got["site/2026/a.txt"].Data) != "aaaa" || string(got["site/b.txt"].Data) != "bb" {
		t.Fatalf("objects = %v", got)
	}
	if _, err := os.Stat(r.o.MediaDir); err == nil {
		t.Error("the local media dir was created")
	}

	r2 := newRestoreRig(t)
	r2.o.MediaStore = r.o.MediaStore
	_, err := r2.dry(t, buildArchive(t, noPlugins))
	wantRefusal(t, err, "media object storage prefix is not empty")
}
