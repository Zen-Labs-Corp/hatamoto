package server

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	signedNameTTL = 60 * time.Second
	signedNameCap = 10000
)

type signedNameKey struct{ commenter, entity uuid.UUID }

type signedNameEntry struct {
	name    string
	expires time.Time
}

// signedNames memoises comment.Service.LastAuthorName for the "Posting as"
// line of a locked comment form, so a cached page costs at most one indexed
// read per signed-in visitor per minute.
type signedNames struct {
	last func(ctx context.Context, commenter, entity uuid.UUID) (string, error)
	now  func() time.Time
	mu   sync.Mutex
	m    map[signedNameKey]signedNameEntry
}

func newSignedNames(last func(context.Context, uuid.UUID, uuid.UUID) (string, error), now func() time.Time) *signedNames {
	if now == nil {
		now = time.Now
	}
	return &signedNames{last: last, now: now, m: map[signedNameKey]signedNameEntry{}}
}

// get returns the name this commenter last posted under on an entity of the
// same schema; "" when there is none. A hit is served from memory (an empty
// answer too). A lookup error returns "" and is not cached, so the form shows
// its fallback line, never an error page. A nil receiver answers "".
func (n *signedNames) get(ctx context.Context, commenter, entity uuid.UUID) string {
	if n == nil || n.last == nil {
		return ""
	}
	k := signedNameKey{commenter, entity}
	n.mu.Lock()
	e, ok := n.m[k]
	n.mu.Unlock()
	if ok && n.now().Before(e.expires) {
		return e.name
	}
	name, err := n.last(ctx, commenter, entity)
	if err != nil {
		return ""
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.now()
	if len(n.m) >= signedNameCap {
		for k, e := range n.m {
			if !now.Before(e.expires) {
				delete(n.m, k)
			}
		}
		if len(n.m) >= signedNameCap { // all live: start over rather than grow
			clear(n.m)
		}
	}
	n.m[k] = signedNameEntry{name: name, expires: now.Add(signedNameTTL)}
	return name
}

// forget drops every entry of the commenter, after they posted: the name the
// hook just gave must show on their next view. Nil-safe.
func (n *signedNames) forget(commenter uuid.UUID) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for k := range n.m {
		if k.commenter == commenter {
			delete(n.m, k)
		}
	}
}
