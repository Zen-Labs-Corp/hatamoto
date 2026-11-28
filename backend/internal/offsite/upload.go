package offsite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"hatamoto.dev/core/internal/backup"
	"hatamoto.dev/core/internal/backupcrypt"
	"hatamoto.dev/core/internal/runenv"
	"hatamoto.dev/core/internal/s3"
)

// ErrVerify means the uploaded object did not match the local archive.
var ErrVerify = errors.New("uploaded object does not match the local backup")

// Upload copies the archive at path to <prefix><id>.tar, or, when key is not
// nil, an age-encrypted copy to <prefix><id>.tar.age (made in a 0600 temp file
// next to the archive and removed on return; the local archive is untouched).
// The size and SHA-256 are those of the stored bytes. It proves the copy by a
// HEAD (size and the SHA-256 recorded in the object's metadata, which the
// provider checked against the bytes on receipt), and only then prunes the
// bucket to the newest keep. A copy that fails the proof is deleted.
//
// The result is always filled in. When it is OK and the error is not nil, only
// the pruning failed. Errors carry provider status and code, never the key.
func Upload(ctx context.Context, c *s3.Client, prefix, id, path string, keep int, key *backupcrypt.Key) (Result, error) {
	res := Result{At: time.Now().UTC()}
	fail := func(err error) (Result, error) {
		res.OK, res.Error = false, err.Error()
		return res, err
	}
	if runenv.Suppress(ctx, nil, runenv.OffsiteBackup, "id", id) {
		return fail(runenv.ErrSuppressed)
	}
	if !backup.ValidID(id) {
		return fail(fmt.Errorf("invalid backup id %q", id))
	}
	src, ext := path, tarExt
	if key != nil {
		tmp := filepath.Join(filepath.Dir(path), ".tmp-"+id+ageExt)
		_ = os.Remove(tmp) // a stale one from a crash
		if err := key.EncryptFile(path, tmp); err != nil {
			return fail(fmt.Errorf("encrypt backup: %w", err))
		}
		defer os.Remove(tmp)
		src, ext, res.Encrypted = tmp, ageExt, true
	}
	f, err := os.Open(src)
	if err != nil {
		return fail(err)
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return fail(fmt.Errorf("read backup: %w", err))
	}
	res.Size, res.SHA256 = size, hex.EncodeToString(h.Sum(nil))

	objKey := prefix + id + ext
	meta := map[string]string{"sha256": res.SHA256, "hatamoto-id": id}
	if res.Encrypted {
		meta["hatamoto-encrypted"] = "age"
	}
	if err := c.Put(ctx, objKey, f, size, meta); err != nil {
		return fail(err)
	}
	head, err := c.Head(ctx, objKey)
	if err != nil {
		return fail(fmt.Errorf("verify upload: %w", err))
	}
	if head.Size != size || head.Meta["sha256"] != res.SHA256 {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		_ = c.Delete(dctx, objKey)
		cancel()
		return fail(fmt.Errorf("%w: expected %d bytes, found %d", ErrVerify, size, head.Size))
	}
	res.OK = true
	if _, err := Prune(ctx, c, prefix, keep); err != nil {
		return res, fmt.Errorf("uploaded, but pruning the bucket failed: %w", err)
	}
	return res, nil
}
