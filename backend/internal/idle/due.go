package idle

import (
	"log/slog"
	"sync/atomic"
	"time"
)

// DueLookup turns a "earliest due time" query into a Wait nextDue answer. The
// zero value is ready to use.
type DueLookup struct{ failing atomic.Bool }

// Result reports t (nil: nothing due) as a due time. When the query failed it
// reports "due now" instead of "nothing due": the gate holds that to the
// loop's normal cadence, so a database error never defers work to the idle
// beat. The first error of a run of failures is logged at Warn. log may be nil.
func (d *DueLookup) Result(t *time.Time, err error, now time.Time, log *slog.Logger, what string) (time.Time, bool) {
	if err != nil {
		if !d.failing.Swap(true) {
			if log == nil {
				log = slog.Default()
			}
			log.Warn(what+": could not read the next due time; checking at the normal interval", "error", err)
		}
		return now, true
	}
	d.failing.Store(false)
	if t == nil {
		return time.Time{}, false
	}
	return *t, true
}
