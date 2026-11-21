package staticexport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

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
}

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
	err = t.c.PutWithOptions(ctx, t.prefix+p, f, st.Size(), s3.PutOptions{
		ContentType:  e.Type,
		CacheControl: CacheControl(p),
		Meta:         map[string]string{"sha256": e.SHA256},
	})
	if err != nil {
		return fmt.Errorf("upload %s: %w", p, err)
	}
	return nil
}

// Discard removes the staging directory.
func (t *S3Target) Discard(staged string) { _ = os.RemoveAll(staged) }
