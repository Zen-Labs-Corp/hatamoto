package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
)

// MigrateOptions describes one copy of media objects between two stores.
type MigrateOptions struct {
	From, To BlobStore
	// Keys are the objects to copy (see MediaKeys).
	Keys []string
	// DryRun reports what would be copied without writing to To.
	DryRun bool
	// Progress is called after every key with the keys done so far, the total
	// and the bytes copied so far. It may be nil.
	Progress func(done, total int, bytes int64)
}

// MigrateReport is the outcome of Migrate. In a dry run Copied and Bytes count
// what would be copied.
type MigrateReport struct {
	Copied, Skipped, Failed int
	Bytes                   int64
	Errors                  []error
}

// MediaKeys returns the sorted, de-duplicated stored keys of rows: each stored
// file and each variant file.
func MediaKeys(rows []*Media) []string {
	seen := map[string]bool{}
	var keys []string
	add := func(k string) {
		if k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for _, m := range rows {
		add(m.StoragePath)
		for _, v := range m.Variants {
			add(v.Path)
		}
	}
	sort.Strings(keys)
	return keys
}

// Migrate copies every key from o.From to o.To and verifies each copy by
// sha256. A key already present at the destination with the same size and
// sha256 is skipped, so an interrupted run is resumed by running it again.
// Nothing is ever deleted from the source. The error is non-nil only when ctx
// ends the run; per-key failures are in the report.
func Migrate(ctx context.Context, o MigrateOptions) (MigrateReport, error) {
	var rep MigrateReport
	for i, key := range o.Keys {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		n, copied, err := migrateKey(ctx, o, key)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return rep, ctx.Err()
			}
			rep.Failed++
			rep.Errors = append(rep.Errors, fmt.Errorf("%s: %w", key, err))
		case copied:
			rep.Copied++
			rep.Bytes += n
		default:
			rep.Skipped++
		}
		if o.Progress != nil {
			o.Progress(i+1, len(o.Keys), rep.Bytes)
		}
	}
	return rep, nil
}

func migrateKey(ctx context.Context, o MigrateOptions, key string) (size int64, copied bool, err error) {
	srcSum, srcSize, err := hashOf(ctx, o.From, key)
	if err != nil {
		return 0, false, fmt.Errorf("read source: %w", err)
	}
	// A destination that is missing or unreadable is simply copied again.
	if sum, n, err := hashOf(ctx, o.To, key); err == nil && n == srcSize && sum == srcSum {
		return 0, false, nil
	}
	if o.DryRun {
		return srcSize, true, nil
	}
	src, err := o.From.Get(ctx, key)
	if err != nil {
		return 0, false, fmt.Errorf("read source: %w", err)
	}
	defer src.Close()
	if _, err := o.To.Put(ctx, key, src); err != nil {
		return 0, false, fmt.Errorf("write destination: %w", err)
	}
	sum, n, err := hashOf(ctx, o.To, key)
	if err != nil {
		return 0, false, fmt.Errorf("verify destination: %w", err)
	}
	if n != srcSize || sum != srcSum {
		return 0, false, errors.New("verify destination: sha256 mismatch after copy")
	}
	return n, true, nil
}

// hashOf reads key and returns its sha256 (hex) and size. It always reads the
// bytes: stored sha256 metadata does not prove the content is intact.
func hashOf(ctx context.Context, s BlobStore, key string) (string, int64, error) {
	r, err := s.Get(ctx, key)
	if err != nil {
		return "", 0, err
	}
	defer r.Close()
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
