package server

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"os"
	"sync/atomic"
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
	c := newUploadCache(func(ctx context.Context, key string) (*os.File, int64, error) {
		f, size, err := svc.OpenStagedCtx(ctx, key)
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
	c := newUploadCache(svc.OpenStagedCtx)
	defer c.closeAll()
	getsBefore := countGets(fake)

	var got []byte
	const chunk = 256 << 10
	for off := int64(0); ; off += chunk {
		ra, size, err := c.get(context.Background(), "up1", key)
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
	ra, _, err := c.get(context.Background(), "up1", key)
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
		ra, _, err := c.get(context.Background(), id, key)
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
	ra, _, err := c.get(context.Background(), "up1", key)
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

// A download in progress must not hold up the cache: another upload's read
// returns while the first is still being fetched.
func TestUploadCacheDownloadDoesNotBlockOtherKeys(t *testing.T) {
	svc, _, key, _ := stagedS3Upload(t, 4096)
	release := make(chan struct{})
	started := make(chan struct{})
	c := newUploadCache(func(ctx context.Context, k string) (*os.File, int64, error) {
		if k == "slow" {
			close(started)
			<-release
			return nil, 0, io.ErrUnexpectedEOF
		}
		return svc.OpenStagedCtx(ctx, k)
	})
	defer c.closeAll()
	go c.get(context.Background(), "up1", "slow")
	<-started

	done := make(chan error, 1)
	go func() {
		ra, _, err := c.get(context.Background(), "up2", key)
		if err == nil {
			ra.(io.Closer).Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a read of another upload waited for the slow download")
	}
	close(release)
}

// A caller waiting for a download that another call started leaves when its
// own context ends.
func TestUploadCacheWaiterHonoursItsContext(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	c := newUploadCache(func(context.Context, string) (*os.File, int64, error) {
		close(started)
		<-release
		return nil, 0, io.ErrUnexpectedEOF
	})
	defer c.closeAll()
	go c.get(context.Background(), "up1", "k")
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.get(ctx, "up1", "k"); err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	close(release)
}

// An idle file is released by the cache's own timer, without another read.
func TestUploadCacheReleasesIdleFilesWithoutAnotherRead(t *testing.T) {
	svc, _, key, _ := stagedS3Upload(t, 4096)
	c, opened := trackedCache(svc)
	defer c.closeAll()
	c.ttl = 20 * time.Millisecond
	ra, _, err := c.get(context.Background(), "up1", key)
	if err != nil {
		t.Fatal(err)
	}
	ra.(io.Closer).Close()
	deadline := time.Now().Add(10 * time.Second)
	for isOpen((*opened)[0]) {
		if time.Now().After(deadline) {
			t.Fatal("the idle file stayed open")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A caller whose context ends before the download does gets its context error
// and the download goes on: the next call finds the finished file. A failed
// download is not kept.
func TestUploadCacheDownloadOutlivesTheCaller(t *testing.T) {
	release := make(chan struct{})
	var opens atomic.Int32
	c := newUploadCache(func(ctx context.Context, _ string) (*os.File, int64, error) {
		n := opens.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
		if n == 1 {
			return nil, 0, io.ErrUnexpectedEOF
		}
		f, err := os.CreateTemp(t.TempDir(), "up")
		if err != nil {
			return nil, 0, err
		}
		f.WriteString("hello")
		return f, 5, nil
	})
	defer c.closeAll()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := c.get(ctx, "up1", "k"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first caller err = %v, want deadline exceeded", err)
	}
	close(release) // the download finishes after the caller left (and fails)
	var err error
	for i := 0; i < 200; i++ { // wait until the failed entry has left the map
		c.mu.Lock()
		n := len(c.m)
		c.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, _, err = c.get(context.Background(), "up1", "k"); err != nil {
		t.Fatalf("retry after a failed download: %v", err)
	}
	if _, _, err = c.get(context.Background(), "up1", "k"); err != nil || opens.Load() != 2 {
		t.Fatalf("cached read: err=%v opens=%d, want nil and 2", err, opens.Load())
	}
}

// The download is not bound to the first caller's deadline: a slow download
// that outlives it is read from the cache by the second call.
func TestUploadCacheSlowDownloadSurvivesCallerDeadline(t *testing.T) {
	var opens atomic.Int32
	c := newUploadCache(func(ctx context.Context, _ string) (*os.File, int64, error) {
		opens.Add(1)
		select {
		case <-time.After(150 * time.Millisecond):
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
		f, err := os.CreateTemp(t.TempDir(), "up")
		if err != nil {
			return nil, 0, err
		}
		return f, 0, nil
	})
	defer c.closeAll()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := c.get(ctx, "up1", "k"); err == nil {
		t.Fatal("first caller should time out")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if _, _, err := c.get(ctx2, "up1", "k"); err != nil {
		t.Fatalf("second caller: %v", err)
	}
	if opens.Load() != 1 {
		t.Errorf("opens = %d, want 1", opens.Load())
	}
}
