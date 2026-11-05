package offsite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"hatamoto.dev/core/internal/backup"
	"hatamoto.dev/core/internal/s3"
)

// ErrVerify means the uploaded object did not match the local archive.
var ErrVerify = errors.New("uploaded object does not match the local backup")

// Upload copies the archive at path to <prefix><id>.tar, proves the copy by a
// HEAD (size and the SHA-256 recorded in the object's metadata, which the
// provider checked against the bytes on receipt), and only then prunes the
// bucket to the newest keep. A copy that fails the proof is deleted.
//
// The result is always filled in. When it is OK and the error is not nil, only
// the pruning failed. Errors carry provider status and code, never the key.
func Upload(ctx context.Context, c *s3.Client, prefix, id, path string, keep int) (Result, error) {
	res := Result{At: time.Now().UTC()}
	fail := func(err error) (Result, error) {
		res.OK, res.Error = false, err.Error()
		return res, err
	}
	if !backup.ValidID(id) {
		return fail(fmt.Errorf("invalid backup id %q", id))
	}
	f, err := os.Open(path)
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

	key := prefix + id + tarExt
	meta := map[string]string{"sha256": res.SHA256, "hatamoto-id": id}
	if err := c.Put(ctx, key, f, size, meta); err != nil {
		return fail(err)
	}
	head, err := c.Head(ctx, key)
	if err != nil {
		return fail(fmt.Errorf("verify upload: %w", err))
	}
	if head.Size != size || head.Meta["sha256"] != res.SHA256 {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		_ = c.Delete(dctx, key)
		cancel()
		return fail(fmt.Errorf("%w: expected %d bytes, found %d", ErrVerify, size, head.Size))
	}
	res.OK = true
	if _, err := Prune(ctx, c, prefix, keep); err != nil {
		return res, fmt.Errorf("uploaded, but pruning the bucket failed: %w", err)
	}
	return res, nil
}
