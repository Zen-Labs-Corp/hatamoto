package admin

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// sudoTTL is how long a password confirmation keeps a session elevated. It is
// counted from the confirmation and never extended by use.
const sudoTTL = 10 * time.Minute

// Wrong second-factor codes are throttled per session: sudoMaxCodeFails wrong
// codes inside sudoFailWindow lock the session out of sudo for sudoFailWindow.
const (
	sudoMaxCodeFails = 5
	sudoFailWindow   = 15 * time.Minute
)

// codeFails is one session's run of wrong codes.
type codeFails struct {
	n           int
	first       time.Time // when the run started
	lockedUntil time.Time // zero until the run reaches sudoMaxCodeFails
}

// sudoStore remembers which sessions confirmed their password and until when.
// It lives in memory: a restart drops every elevation and users confirm again.
// An entry is inert once its session ID dies (logout, revoke) and is swept on
// the next Grant. The zero value is ready to use.
type sudoStore struct {
	mu    sync.Mutex
	until map[uuid.UUID]time.Time
	fails map[uuid.UUID]*codeFails
	now   func() time.Time // nil means time.Now
}

func (s *sudoStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Grant elevates the session for sudoTTL from now, replacing any earlier window.
func (s *sudoStore) Grant(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	if s.until == nil {
		s.until = map[uuid.UUID]time.Time{}
	}
	for k, t := range s.until {
		if !now.Before(t) {
			delete(s.until, k)
		}
	}
	s.until[id] = now.Add(sudoTTL)
	delete(s.fails, id)
}

// Active reports whether the session is inside its window. It does not extend it.
func (s *sudoStore) Active(id uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.until[id]
	return ok && s.clock().Before(t)
}

// BeginCode reserves one code attempt for the session and reports whether it
// may go ahead. It refuses while the session is locked, or once the attempts
// already counted (failed or still in flight) reach sudoMaxCodeFails. The
// reservation counts as a wrong code from the start, so parallel requests
// cannot run more than sudoMaxCodeFails verifications per window. Settle it:
// a wrong code keeps it, Grant clears it, ReleaseCode gives it back.
func (s *sudoStore) BeginCode(id uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	if s.fails == nil {
		s.fails = map[uuid.UUID]*codeFails{}
	}
	for k, f := range s.fails {
		if !now.Before(f.first.Add(sudoFailWindow)) && !now.Before(f.lockedUntil) {
			delete(s.fails, k)
		}
	}
	f := s.fails[id]
	if f == nil {
		f = &codeFails{first: now}
		s.fails[id] = f
	}
	if now.Before(f.lockedUntil) || f.n >= sudoMaxCodeFails {
		return false
	}
	f.n++
	if f.n >= sudoMaxCodeFails {
		f.lockedUntil = now.Add(sudoFailWindow)
	}
	return true
}

// ReleaseCode gives back a reservation that did not end in a definite wrong
// code: a wrong password, a provider error or a cancelled request.
func (s *sudoStore) ReleaseCode(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.fails[id]
	if f == nil || f.n == 0 {
		return
	}
	f.n--
	if f.n < sudoMaxCodeFails {
		f.lockedUntil = time.Time{}
	}
	if f.n == 0 {
		delete(s.fails, id)
	}
}

// SudoStore is the sudo-mode grants of a process. Share one between Admin
// values to keep elevations when the admin is rebuilt (the address change
// swaps in a new Admin). Use NewSudoStore.
type SudoStore struct{ s sudoStore }

// NewSudoStore returns an empty store.
func NewSudoStore() *SudoStore { return &SudoStore{} }

// sudoState is everything sudo mode keeps on the Admin.
type sudoState struct {
	store *sudoStore

	mu     sync.RWMutex
	factor SecondFactor
}

// secondFactor is the registered provider, or nil.
func (s *sudoState) secondFactor() SecondFactor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.factor
}
