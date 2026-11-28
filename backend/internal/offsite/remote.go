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

const (
	tarExt = ".tar"
	ageExt = ".tar.age"
)

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

// RemoteBackup is one backup found in the bucket. Encrypted is true when the
// object is an age file (<id>.tar.age).
type RemoteBackup struct {
	ID           string
	Key          string
	Size         int64
	LastModified time.Time
	Encrypted    bool
}

// remoteID returns the backup id of an object key and whether it is the
// encrypted form, or ok false when the key is not "<prefix><ValidID>.tar" or
// "<prefix><ValidID>.tar.age" exactly (nested keys and foreign files).
func remoteID(prefix, key string) (id string, encrypted, ok bool) {
	rest, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return "", false, false
	}
	id, encrypted = strings.CutSuffix(rest, ageExt)
	if !encrypted {
		if id, ok = strings.CutSuffix(rest, tarExt); !ok {
			return "", false, false
		}
	}
	if strings.Contains(id, "/") || !backup.ValidID(id) {
		return "", false, false
	}
	return id, encrypted, true
}

// listKeys returns every backup object under prefix grouped by id, and the
// ids newest first.
func listKeys(ctx context.Context, c *s3.Client, prefix string) (map[string][]RemoteBackup, []string, error) {
	objs, err := c.List(ctx, prefix)
	if err != nil {
		return nil, nil, err
	}
	byID := map[string][]RemoteBackup{}
	for _, o := range objs {
		if id, enc, ok := remoteID(prefix, o.Key); ok {
			byID[id] = append(byID[id], RemoteBackup{ID: id, Key: o.Key, Size: o.Size, LastModified: o.LastModified, Encrypted: enc})
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	return byID, ids, nil
}

// ListRemote returns the backups under prefix, newest id first, one row per
// id. When an id exists under both suffixes the encrypted object is listed.
func ListRemote(ctx context.Context, c *s3.Client, prefix string) ([]RemoteBackup, error) {
	byID, ids, err := listKeys(ctx, c, prefix)
	if err != nil {
		return nil, err
	}
	var out []RemoteBackup
	for _, id := range ids {
		row := byID[id][0]
		for _, b := range byID[id] {
			if b.Encrypted {
				row = b
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// Prune deletes the backups under prefix beyond the newest keep ids and
// returns the deleted keys. An id counts once however many suffixes it has,
// and every key of a pruned id goes. Only keys shaped
// "<prefix><ValidID>.tar[.age]" are considered; everything else in the bucket
// is left alone.
func Prune(ctx context.Context, c *s3.Client, prefix string, keep int) (deleted []string, err error) {
	if keep < 1 {
		return nil, errors.New("keep must be at least 1")
	}
	byID, ids, err := listKeys(ctx, c, prefix)
	if err != nil {
		return nil, err
	}
	if len(ids) <= keep {
		return nil, nil
	}
	var errs []error
	for _, id := range ids[keep:] {
		for _, b := range byID[id] {
			if derr := c.Delete(ctx, b.Key); derr != nil {
				errs = append(errs, derr)
				continue
			}
			deleted = append(deleted, b.Key)
		}
	}
	return deleted, errors.Join(errs...)
}

// Download fetches backup id into destDir/.tmp-<id>.tar.age (preferred) or
// destDir/.tmp-<id>.tar and returns the path. The SHA-256 of the bytes must equal the one recorded on the object when it
// was uploaded (for an encrypted object that is the ciphertext, which is
// stored as is); on any mismatch or error the file is removed. The caller
// removes the file when done.
func Download(ctx context.Context, c *s3.Client, prefix, id, destDir string) (_ string, err error) {
	if !backup.ValidID(id) {
		return "", fmt.Errorf("invalid backup id %q", id)
	}
	ext := ageExt
	key := prefix + id + ext
	head, err := c.Head(ctx, key)
	if err != nil && s3.NotFound(err) {
		ext = tarExt
		key = prefix + id + ext
		head, err = c.Head(ctx, key)
	}
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
	tmp := filepath.Join(destDir, ".tmp-"+id+ext)
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
