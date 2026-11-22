package reviewstate

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// MaxPending is the most rows one Pending call returns.
const MaxPending = 1000

// ErrUnavailable is returned by Pending outside serve, where there is no database.
var ErrUnavailable = errors.New("review: no database in this process")

// PendingStage is one staged edit waiting at a stage. Times are UTC.
type PendingStage struct {
	ChangesetID int64  // the admin page is <admin>/review/<ChangesetID>
	EntityID    string // uuid of the entry
	EntityName  string // the name in the staged change
	Schema      string // schema slug
	Requester   string // user id of the requester
	Stage       Stage  // the stage waiting for a decision
	Index       int    // 0-based: "Stage Index+1 of Total"
	Total       int
	RequestedAt time.Time // when the change was sent
	EnteredAt   time.Time // when it reached Stage (RequestedAt for stage 0)
}

// PendingSource reads at most limit pending stages, oldest first; core only.
type PendingSource func(ctx context.Context, limit int) ([]PendingStage, error)

var pendingSrc atomic.Pointer[PendingSource]

// SetPendingSource installs the reader. Core calls it in serve, after Main
// froze the registrations, so Freeze does not cover it. It panics on nil.
func SetPendingSource(fn PendingSource) {
	if fn == nil {
		panic("reviewstate: nil pending source")
	}
	pendingSrc.Store(&fn)
}

// Pending lists the edits waiting at a stage, oldest first, at most
// MaxPending. It checks no rights: it is a system read.
func Pending(ctx context.Context) ([]PendingStage, error) {
	p := pendingSrc.Load()
	if p == nil {
		return nil, ErrUnavailable
	}
	return (*p)(ctx, MaxPending)
}
