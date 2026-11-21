// Package frameticket stores the admin's one-time frame tickets. The admin
// signs and checks them; this package only keeps the unspent records, in
// memory (tests, tools) or in Postgres (a server, so any server can spend a
// ticket another minted).
package frameticket

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// MaxTickets is how many unspent tickets are kept; the newest win.
	MaxTickets = 256
	// MaxBytes bounds the payload bytes of all kept tickets at once.
	MaxBytes = 32 << 20
)

// Ticket is one minted, unspent ticket. The HMAC tag is never stored.
type Ticket struct {
	Nonce   string // base64url of the 8-byte nonce: the key
	Purpose string
	Session uuid.UUID
	Target  string
	Expiry  time.Time
	Payload []byte // nil stays nil
}

// Backend keeps unspent tickets. Take removes the record on any hit, expired
// or not, and reports ok only for one that has not expired at now.
type Backend interface {
	Put(ctx context.Context, t Ticket, now time.Time) error
	Take(ctx context.Context, nonce string, now time.Time) (Ticket, bool, error)
}

// Memory is the in-process Backend. The zero value is ready to use.
type Memory struct {
	mu sync.Mutex
	m  map[string]Ticket
}

// Put sweeps expired tickets, evicts the oldest while the count or byte bound
// would be exceeded, then keeps t.
func (s *Memory) Put(_ context.Context, t Ticket, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]Ticket{}
	}
	total := len(t.Payload)
	for k, v := range s.m {
		if now.After(v.Expiry) {
			delete(s.m, k)
			continue
		}
		total += len(v.Payload)
	}
	for len(s.m) > 0 && (len(s.m) >= MaxTickets || total > MaxBytes) { // evict the oldest
		var oldest string
		var at time.Time
		for k, v := range s.m {
			if oldest == "" || v.Expiry.Before(at) {
				oldest, at = k, v.Expiry
			}
		}
		total -= len(s.m[oldest].Payload)
		delete(s.m, oldest)
	}
	s.m[t.Nonce] = t
	return nil
}

// Take removes and returns the ticket under nonce; ok is false for a miss or
// an expired ticket (which is removed all the same).
func (s *Memory) Take(_ context.Context, nonce string, now time.Time) (Ticket, bool, error) {
	s.mu.Lock()
	rec, ok := s.m[nonce]
	delete(s.m, nonce)
	s.mu.Unlock()
	if !ok || now.After(rec.Expiry) {
		return Ticket{}, false, nil
	}
	return rec, true, nil
}

// Len is the number of kept tickets.
func (s *Memory) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}
