package server

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/s3/s3test"
)

// The uploadCache seam is the media service's own OpenStaged, so these tests
// count the real downloads from the bucket without a database.
func stagedS3Upload(t *testing.T, size int) (*media.Service, *s3test.Server, string, []byte) {
	t.Helper()
	fake := s3test.NewServer(t)
	t.Setenv("TMPDIR", t.TempDir())
	store, err := media.NewS3Store(media.S3Target{Endpoint: fake.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		Prefix: "site/", AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := media.NewService(nil, media.Config{UploadDir: t.TempDir(), MaxSizeMB: 5, Store: store}, slog.Default())
	// Noise does not compress: a side of about sqrt(size/4) pixels makes a PNG
	// of roughly size bytes.
	side := int(math.Sqrt(float64(size) / 4))
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	rand.New(rand.NewSource(1)).Read(img.Pix)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	key, err := svc.StageBlob(context.Background(), uuid.New(), bytes.NewReader(data), "data.png")
	if err != nil {
		t.Fatal(err)
	}
	return svc, fake, key, data
}

// trackedCache is a cache over svc whose opened files are recorded, so a test
// can tell which are still open (the spool files are unlinked: the directory
// listing cannot).
func trackedCache(svc *media.Service) (*uploadCache, *[]*os.File) {
	var opened []*os.File
	c := newUploadCache(func(key string) (*os.File, int64, error) {
		f, size, err := svc.OpenStaged(key)
		opened = append(opened, f)
		return f, size, err
	})
	return c, &opened
}

func isOpen(f *os.File) bool {
	_, err := f.Stat()
	return err == nil
}

func TestUploadCacheStreamsTheStagedObjectOnce(t *testing.T) {
	svc, fake, key, data := stagedS3Upload(t, 1<<20+(256<<10))
	c := newUploadCache(svc.OpenStaged)
	defer c.closeAll()
	getsBefore := countGets(fake)

	var got []byte
	const chunk = 256 << 10
	for off := int64(0); ; off += chunk {
		ra, size, err := c.get("up1", key)
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, chunk)
		n, err := ra.ReadAt(buf, off)
		ra.(io.Closer).Close() // what the host does after each call
		got = append(got, buf[:n]...)
		if err == io.EOF || off+chunk >= size {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(got, data) {
		t.Errorf("read %d bytes, want %d identical", len(got), len(data))
	}
	if n := countGets(fake) - getsBefore; n != 1 {
		t.Errorf("GETs for %d chunks = %d, want 1", len(data)/chunk+1, n)
	}
}

func TestUploadCacheEvictsIdleFiles(t *testing.T) {
	svc, _, key, _ := stagedS3Upload(t, 4096)
	c, opened := trackedCache(svc)
	clock := time.Now()
	c.now = func() time.Time { return clock }
	ra, _, err := c.get("up1", key)
	if err != nil {
		t.Fatal(err)
	}
	ra.(io.Closer).Close()
	if !isOpen((*opened)[0]) {
		t.Fatal("the file closed while cached")
	}
	clock = clock.Add(c.ttl + time.Second)
	c.mu.Lock()
	c.evict(c.now())
	c.mu.Unlock()
	if len(c.m) != 0 || isOpen((*opened)[0]) {
		t.Errorf("after the TTL: %d entries, file open: %v", len(c.m), isOpen((*opened)[0]))
	}
}

func TestUploadCacheKeepsAtMostMaxEntries(t *testing.T) {
	svc, _, key, _ := stagedS3Upload(t, 4096)
	c, opened := trackedCache(svc)
	defer c.closeAll()
	c.max = 2
	clock := time.Now()
	c.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	for _, id := range []string{"a", "b", "c"} {
		ra, _, err := c.get(id, key)
		if err != nil {
			t.Fatal(err)
		}
		ra.(io.Closer).Close()
	}
	if len(c.m) != 2 || c.m["a\x00"+key] != nil || isOpen((*opened)[0]) || !isOpen((*opened)[1]) || !isOpen((*opened)[2]) {
		t.Errorf("entries = %d (oldest kept: %v)", len(c.m), c.m["a\x00"+key] != nil)
	}
}

// A file the cache drops while a call is still reading it closes when that
// call lets go.
func TestUploadCacheDefersClosingAFileInUse(t *testing.T) {
	svc, _, key, data := stagedS3Upload(t, 4096)
	c, opened := trackedCache(svc)
	ra, _, err := c.get("up1", key)
	if err != nil {
		t.Fatal(err)
	}
	c.closeAll()
	buf := make([]byte, 16)
	if _, err := ra.ReadAt(buf, 0); err != nil || !bytes.Equal(buf, data[:16]) {
		t.Errorf("ReadAt after drop = %v", err)
	}
	ra.(io.Closer).Close()
	if isOpen((*opened)[0]) {
		t.Error("the file is still open after the last reader closed")
	}
}
