package offsite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"hatamoto.dev/core/internal/backup"
	"hatamoto.dev/core/internal/s3"
)

const tarExt = ".tar"

// Target locates and opens a bucket. It carries the opened secret key, so it
// is built per use and never stored, logged or printed.
type Target struct {
	Endpoint  string
	Region    string
	Bucket    string
	Prefix    string
	AccessKey string
	SecretKey string
}

// Validate checks the target the way saved settings are checked.
func (t Target) Validate() error {
	if err := validateTarget(t.Endpoint, t.Region, t.Bucket, t.Prefix, t.AccessKey); err != nil {
		return err
	}
	if t.SecretKey == "" {
		return errors.New("secret key is required")
	}
	return nil
}

// Client returns an S3 client for the target. h may be nil.
func (t Target) Client(h *http.Client) (*s3.Client, error) {
	return s3.New(s3.Config{Endpoint: t.Endpoint, Region: t.Region, Bucket: t.Bucket,
		AccessKey: t.AccessKey, SecretKey: t.SecretKey, HTTP: h})
}

// Target returns the connection details with the opened secret key; ok is
// false when no secret is saved or it cannot be opened.
func (s Settings) Target(key []byte) (Target, bool) {
	secret, ok := s.Secret(key)
	if !ok {
		return Target{}, false
	}
	return Target{Endpoint: s.EffectiveEndpoint(), Region: s.Region, Bucket: s.Bucket,
		Prefix: s.Prefix, AccessKey: s.AccessKey, SecretKey: secret}, true
}

// RemoteBackup is one backup archive found in the bucket.
type RemoteBackup struct {
	ID           string
	Key          string
	Size         int64
	LastModified time.Time
}

// remoteID returns the backup id of an object key, or false when the key is
// not "<prefix><ValidID>.tar" exactly (nested keys and foreign files).
func remoteID(prefix, key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return "", false
	}
	id, ok := strings.CutSuffix(rest, tarExt)
	if !ok || strings.Contains(id, "/") || !backup.ValidID(id) {
		return "", false
	}
	return id, true
}

// ListRemote returns the backups under prefix, newest id first.
func ListRemote(ctx context.Context, c *s3.Client, prefix string) ([]RemoteBackup, error) {
	objs, err := c.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var out []RemoteBackup
	for _, o := range objs {
		if id, ok := remoteID(prefix, o.Key); ok {
			out = append(out, RemoteBackup{ID: id, Key: o.Key, Size: o.Size, LastModified: o.LastModified})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// Prune deletes the backups under prefix beyond the newest keep and returns
// the deleted keys. Only keys shaped "<prefix><ValidID>.tar" are considered;
// everything else in the bucket is left alone.
func Prune(ctx context.Context, c *s3.Client, prefix string, keep int) (deleted []string, err error) {
	if keep < 1 {
		return nil, errors.New("keep must be at least 1")
	}
	all, err := ListRemote(ctx, c, prefix)
	if err != nil {
		return nil, err
	}
	if len(all) <= keep {
		return nil, nil
	}
	var errs []error
	for _, b := range all[keep:] {
		if derr := c.Delete(ctx, b.Key); derr != nil {
			errs = append(errs, derr)
			continue
		}
		deleted = append(deleted, b.Key)
	}
	return deleted, errors.Join(errs...)
}

// Download fetches backup id into destDir/.tmp-<id>.tar and returns the path.
// The SHA-256 of the bytes must equal the one recorded on the object when it
// was uploaded; on any mismatch or error the file is removed. The caller
// removes the file when done.
func Download(ctx context.Context, c *s3.Client, prefix, id, destDir string) (_ string, err error) {
	if !backup.ValidID(id) {
		return "", fmt.Errorf("invalid backup id %q", id)
	}
	key := prefix + id + tarExt
	head, err := c.Head(ctx, key)
	if err != nil {
		if s3.NotFound(err) {
			return "", fmt.Errorf("backup %s is not in the bucket", id)
		}
		return "", err
	}
	want := head.Meta["sha256"]
	if want == "" {
		return "", fmt.Errorf("backup %s has no recorded checksum; it was not uploaded by hatamoto", id)
	}
	body, _, err := c.Get(ctx, key)
	if err != nil {
		return "", err
	}
	defer body.Close()
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", err
	}
	tmp := filepath.Join(destDir, ".tmp-"+id+tarExt)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			os.Remove(tmp)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("download %s: %w", id, err)
	}
	if n != head.Size {
		return "", fmt.Errorf("download %s: got %d bytes, the bucket reports %d", id, n, head.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return "", fmt.Errorf("download %s: SHA-256 differs from the recorded checksum", id)
	}
	return tmp, nil
}
