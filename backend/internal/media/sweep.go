package media

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
)

// MinSweepAge is the youngest age a sweep accepts: an object this fresh may
// belong to an upload whose row is not written yet.
const MinSweepAge = time.Hour

// SweepStore is the part of a store a sweep needs.
type SweepStore interface {
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
	Delete(ctx context.Context, key string) error
}

// SweepOptions configures Sweep.
type SweepOptions struct {
	// OlderThan is how old an object must be to count as an orphan; at least
	// MinSweepAge.
	OlderThan time.Duration
	// Apply deletes the orphans. Without it Sweep only reports them.
	Apply bool
	// Now is the clock; the zero value means time.Now().
	Now time.Time
}

// SweepResult is the outcome of Sweep. Orphans, Bytes and Deleted count what
// was found and what was removed; Failed holds one error per key that could
// not be deleted.
type SweepResult struct {
	Orphans []ObjectInfo
	Bytes   int64
	Deleted int
	Failed  []error
}

// Sweep finds objects under the media directory of st that no row refers to
// and that are older than o.OlderThan, and deletes them when o.Apply is set.
// Only keys under the media directory are looked at, never the staged files
// (the changeset sweeper owns those) and never an object without a
// modification time. loadKeys runs after the listing, so a row written while
// the bucket was being listed is seen. The error is non-nil when the run could
// not start; per-key delete failures are in the result.
func Sweep(ctx context.Context, st SweepStore, loadKeys func(context.Context) (map[string]struct{}, error), o SweepOptions) (SweepResult, error) {
	var res SweepResult
	if o.OlderThan < MinSweepAge {
		return res, fmt.Errorf("media sweep: the minimum age is %s", MinSweepAge)
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.Add(-o.OlderThan)
	listed, err := st.List(ctx, legacyMediaTenantDir+"/")
	if err != nil {
		return res, fmt.Errorf("media sweep: list: %w", err)
	}
	referenced, err := loadKeys(ctx)
	if err != nil {
		return res, fmt.Errorf("media sweep: load media rows: %w", err)
	}
	for _, obj := range listed {
		switch {
		case !strings.HasPrefix(obj.Key, legacyMediaTenantDir+"/"), isStagedKey(obj.Key):
		case obj.ModTime.IsZero(), obj.ModTime.After(cutoff):
		default:
			if _, ok := referenced[obj.Key]; !ok {
				res.Orphans = append(res.Orphans, obj)
				res.Bytes += obj.Size
			}
		}
	}
	sort.Slice(res.Orphans, func(i, j int) bool { return res.Orphans[i].Key < res.Orphans[j].Key })
	if !o.Apply {
		return res, nil
	}
	for _, obj := range res.Orphans {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if err := st.Delete(ctx, obj.Key); err != nil && !errors.Is(err, fs.ErrNotExist) {
			res.Failed = append(res.Failed, fmt.Errorf("%s: %w", obj.Key, err))
			continue
		}
		res.Deleted++
	}
	return res, nil
}
