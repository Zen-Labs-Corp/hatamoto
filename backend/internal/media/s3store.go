package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"hatamoto.dev/core/internal/s3"
)

// metaSHA256 is the user-metadata key under which Put records the sha256 of
// the bytes, so migrate and backup can verify an object without reading it.
const metaSHA256 = "sha256"

// S3Store keeps media bytes in S3-compatible object storage. The object key is
// the target prefix followed by the media key, verbatim. The client needs the
// body as a ReaderAt of known size and has no ranged reads, so Put and Open
// spool through a temporary file on local disk.
type S3Store struct {
	client  *s3.Client
	prefix  string
	baseURL string
}

var (
	_ BlobStore = (*S3Store)(nil)
	_ Stater    = (*S3Store)(nil)
	_ Lister    = (*S3Store)(nil)
	_ Opener    = (*S3Store)(nil)
)

// NewS3Store returns a store for t whose URLs start with baseURL. It makes no
// network call: an unreachable bucket shows up on first use. httpc may be nil.
func NewS3Store(t S3Target, baseURL string, httpc *http.Client) (*S3Store, error) {
	c, err := s3.New(s3.Config{Endpoint: t.Endpoint, Region: t.Region, Bucket: t.Bucket,
		AccessKey: t.AccessKey, SecretKey: t.SecretKey, HTTP: httpc})
	if err != nil {
		return nil, fmt.Errorf("media object storage: %w", err)
	}
	return &S3Store{client: c, prefix: t.Prefix, baseURL: baseURL}, nil
}

func (s *S3Store) URL(key string) string { return s.baseURL + "/" + key }

func (s *S3Store) objectKey(key string) (string, error) {
	if err := checkKey(key); err != nil {
		return "", err
	}
	return s.prefix + key, nil
}

// notExist maps a provider 404 to fs.ErrNotExist.
func notExist(op, key string, err error) error {
	if s3.NotFound(err) {
		return fmt.Errorf("%s blob %q: %w", op, key, fs.ErrNotExist)
	}
	return err
}

func (s *S3Store) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	obj, err := s.objectKey(key)
	if err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp("", "hatamoto-media-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), r)
	if err != nil {
		return 0, err
	}
	meta := map[string]string{metaSHA256: hex.EncodeToString(h.Sum(nil))}
	if err := s.client.Put(ctx, obj, tmp, size, meta); err != nil {
		return 0, err
	}
	return size, nil
}

func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.objectKey(key)
	if err != nil {
		return nil, err
	}
	body, _, err := s.client.Get(ctx, obj)
	if err != nil {
		return nil, notExist("get", key, err)
	}
	return body, nil
}

// GetWithInfo streams key; the details come from the same response as the bytes.
func (s *S3Store) GetWithInfo(ctx context.Context, key string) (ObjectInfo, io.ReadCloser, error) {
	obj, err := s.objectKey(key)
	if err != nil {
		return ObjectInfo{}, nil, err
	}
	body, o, err := s.client.Get(ctx, obj)
	if err != nil {
		return ObjectInfo{}, nil, notExist("get", key, err)
	}
	return objectInfo(key, o), body, nil
}

// Delete removes key. The client treats a missing key as success, so Head
// first to keep the BlobStore contract.
func (s *S3Store) Delete(ctx context.Context, key string) error {
	obj, err := s.objectKey(key)
	if err != nil {
		return err
	}
	if _, err := s.client.Head(ctx, obj); err != nil {
		return notExist("delete", key, err)
	}
	return s.client.Delete(ctx, obj)
}

func objectInfo(key string, o s3.Object) ObjectInfo {
	etag := ""
	switch {
	case o.ETag != "":
		etag = `"` + o.ETag + `"`
	case o.Meta[metaSHA256] != "":
		etag = `"` + o.Meta[metaSHA256] + `"`
	}
	return ObjectInfo{Key: key, Size: o.Size, ETag: etag, ModTime: o.LastModified, SHA256: o.Meta[metaSHA256]}
}

func (s *S3Store) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	obj, err := s.objectKey(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	o, err := s.client.Head(ctx, obj)
	if err != nil {
		return ObjectInfo{}, notExist("stat", key, err)
	}
	return objectInfo(key, o), nil
}

func (s *S3Store) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	objs, err := s.client.List(ctx, s.prefix+prefix)
	if err != nil {
		return nil, err
	}
	out := make([]ObjectInfo, 0, len(objs))
	for _, o := range objs {
		out = append(out, objectInfo(strings.TrimPrefix(o.Key, s.prefix), o))
	}
	return out, nil
}

// Open spools the object to a temporary file and returns it for random
// access. Closing it removes the file.
func (s *S3Store) Open(ctx context.Context, key string) (ObjectInfo, io.ReadSeekCloser, error) {
	obj, err := s.objectKey(key)
	if err != nil {
		return ObjectInfo{}, nil, err
	}
	body, o, err := s.client.Get(ctx, obj)
	if err != nil {
		return ObjectInfo{}, nil, notExist("open", key, err)
	}
	defer body.Close()
	f, err := os.CreateTemp("", "hatamoto-media-*")
	if err != nil {
		return ObjectInfo{}, nil, err
	}
	if _, err := io.Copy(f, body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return ObjectInfo{}, nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		os.Remove(f.Name())
		return ObjectInfo{}, nil, err
	}
	return objectInfo(key, o), &spoolFile{f}, nil
}

// Test checks that the bucket is reachable with the configured credentials.
func (s *S3Store) Test(ctx context.Context) error { return s.client.Test(ctx) }

// spoolFile is a temporary file that deletes itself on Close.
type spoolFile struct{ *os.File }

func (f *spoolFile) Close() error {
	err := f.File.Close()
	if rerr := os.Remove(f.Name()); err == nil && !os.IsNotExist(rerr) {
		err = rerr
	}
	return err
}
