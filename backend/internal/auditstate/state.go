// Package auditstate holds the audit types and the single sink registration
// that the public hatamoto.dev/core/audit package exposes. Only core calls
// Freeze and Reset; wrapper modules use the public package.
package auditstate

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
)

// Event is one audit record.
type Event struct {
	ActorID   string // user uuid; "" when none
	Actor     string // display actor as stored: email, "cli", "system", "api", ...
	ActorKind string // user|api_key|plugin|system|cli
	KeyID     string // API key uuid; "" when none
	Action    string
	Target    string
	Detail    map[string]any // never a secret
	IP        string
	UserAgent string
	RequestID string
	Result    string // ok|denied|error; "" means ok
}

// Sink receives every audit event after core has written its own table row.
// tx is the business transaction when core has one (an error then rolls the
// change back) and nil otherwise.
type Sink interface {
	Record(ctx context.Context, tx pgx.Tx, ev Event) error
}

var (
	// ErrNoTx is returned by the public Record when no transaction is given.
	ErrNoTx = errors.New("audit: Record needs a transaction")
	// ErrTable wraps a failed default table write.
	ErrTable = errors.New("audit: table write failed")
	// ErrSink wraps an error from the registered sink.
	ErrSink = errors.New("audit: registered sink failed")
)

var (
	mu     sync.Mutex
	frozen bool
	sink   atomic.Pointer[Sink]
)

// Register installs s. It panics after Freeze, on a second call, or with a
// nil (or typed-nil) sink.
func Register(s Sink) {
	mu.Lock()
	defer mu.Unlock()
	switch {
	case frozen:
		panic("audit: Register called after app.Main started; register from an init function")
	case sink.Load() != nil:
		panic("audit: Register called twice")
	case isNil(s):
		panic("audit: Register with a nil sink")
	}
	sink.Store(&s)
}

// isNil reports whether s is nil or an interface holding a nil pointer, map,
// slice, func or chan, which would panic on first use.
func isNil(s Sink) bool {
	if s == nil {
		return true
	}
	switch v := reflect.ValueOf(s); v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		return v.IsNil()
	}
	return false
}

// Registered returns the registered sink, or nil.
func Registered() Sink {
	if p := sink.Load(); p != nil {
		return *p
	}
	return nil
}

// Freeze makes any later Register panic. Core calls it first thing in Main.
func Freeze() {
	mu.Lock()
	frozen = true
	mu.Unlock()
}

// Reset clears all state; tests only.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	frozen = false
	sink.Store(nil)
}
