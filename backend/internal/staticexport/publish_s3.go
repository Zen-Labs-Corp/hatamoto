package staticexport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/s3"
)

// S3Client is the part of the S3 client the target needs.
type S3Client interface {
	PutWithOptions(ctx context.Context, key string, r io.ReaderAt, size int64, o s3.PutOptions) error
	Get(ctx context.Context, key string) (io.ReadCloser, s3.Object, error)
	Delete(ctx context.Context, key string) error
}

const s3PutConcurrency = 4

// S3Target publishes to a bucket prefix. It deletes only keys that the
// previous manifest lists (so only keys an export wrote), and never lists the
// bucket.
type S3Target struct {
	c        S3Client
	prefix   string
	describe string
	key      string
	puts     atomic.Int64
	dels     atomic.Int64
}

// Modified reports whether the last Publish uploaded or deleted at least one
// object. An S3 publish is not atomic, so after an error the bucket may be
// part way to the new export.
func (t *S3Target) Modified() bool { return t.puts.Load() > 0 || t.dels.Load() > 0 }

// Deleted reports whether the last Publish deleted at least one object.
func (t *S3Target) Deleted() bool { return t.dels.Load() > 0 }

// NewS3Target returns a target for prefix in bucket. endpoint only labels
// the run record.
func NewS3Target(c S3Client, endpoint, bucket, prefix string) *S3Target {
	return &S3Target{
		c: c, prefix: prefix,
		describe: "s3://" + bucket + "/" + prefix,
		key:      "s3:" + endpoint + "/" + bucket + "/" + prefix,
	}
}

func (t *S3Target) Key() string      { return t.key }
func (t *S3Target) Describe() string { return t.describe }

// Stage returns a fresh temporary directory.
func (t *S3Target) Stage(context.Context) (string, error) {
	return os.MkdirTemp("", "hatamoto-static-*")
}

// Previous reads the manifest object. A missing object means a first export.
func (t *S3Target) Previous(ctx context.Context) (Manifest, bool, error) {
	rc, _, err := t.c.Get(ctx, t.prefix+ManifestFile)
	if s3.NotFound(err) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, fmt.Errorf("read previous manifest: %w", err)
	}
	defer rc.Close()
	m, err := ReadManifest(rc)
	if err != nil {
		return Manifest{}, false, err
	}
	return m, true, nil
}

// Publish uploads the changed files, then deletes the removed ones, then
// writes the manifest object. An upload error returns before any delete.
func (t *S3Target) Publish(ctx context.Context, staged string, d Diff, next Manifest) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	t.puts.Store(0)
	t.dels.Store(0)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	sem := make(chan struct{}, s3PutConcurrency)
	for _, p := range d.Changed {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := t.put(ctx, staged, p, next.Files[p]); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	for _, p := range d.Removed {
		if err := t.c.Delete(ctx, t.prefix+p); err != nil {
			return fmt.Errorf("delete %s: %w", p, err)
		}
		t.dels.Add(1)
	}

	body, err := next.Encode()
	if err != nil {
		return err
	}
	err = t.c.PutWithOptions(ctx, t.prefix+ManifestFile, bytes.NewReader(body), int64(len(body)), s3.PutOptions{
		ContentType:  "application/json",
		CacheControl: CacheControl(ManifestFile),
	})
	if err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

func (t *S3Target) put(ctx context.Context, staged, p string, e Entry) error {
	f, err := os.Open(filepath.Join(staged, filepath.FromSlash(p)))
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.IsDir() {
		return errors.New("staged path is a directory: " + p)
	}
	o := s3.PutOptions{
		CacheControl: CacheControl(p),
		Meta:         map[string]string{"sha256": e.SHA256},
	}
	o.ContentType, o.ContentDisposition = mediaObjectType(p, e.Type)
	err = t.c.PutWithOptions(ctx, t.prefix+p, f, st.Size(), o)
	if err != nil {
		return fmt.Errorf("upload %s: %w", p, err)
	}
	t.puts.Add(1)
	return nil
}

// mediaObjectType mirrors the live /media answer: a type outside the upload
// allowlist is served as an attachment. CSP, nosniff and frame headers cannot be
// object metadata; the docs send them to the CDN.
func mediaObjectType(p, ct string) (string, string) {
	if !strings.HasPrefix(p, "media/") {
		return ct, ""
	}
	mt, _, err := mime.ParseMediaType(ct)
	if _, ok := media.AllowedExtensionsForMIME(mt); err == nil && ok {
		return ct, ""
	}
	return "application/octet-stream", "attachment"
}

// Discard removes the staging directory.
func (t *S3Target) Discard(staged string) { _ = os.RemoveAll(staged) }
