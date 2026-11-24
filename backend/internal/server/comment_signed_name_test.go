package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type nameRig struct {
	n     *signedNames
	calls int
	err   error
	name  string
	now   time.Time
}

func newNameRig() *nameRig {
	r := &nameRig{name: "story_author", now: time.Now()}
	r.n = newSignedNames(func(context.Context, uuid.UUID, uuid.UUID) (string, error) {
		r.calls++
		return r.name, r.err
	}, func() time.Time { return r.now })
	return r
}

func TestSignedNameMemoisedWithinTTL(t *testing.T) {
	r := newNameRig()
	c, e := uuid.New(), uuid.New()
	for range 3 {
		if got := r.n.get(context.Background(), c, e); got != "story_author" {
			t.Fatalf("get = %q", got)
		}
	}
	if r.calls != 1 {
		t.Errorf("lookups within the TTL = %d, want 1", r.calls)
	}
	r.now = r.now.Add(signedNameTTL + time.Second)
	r.n.get(context.Background(), c, e)
	if r.calls != 2 {
		t.Errorf("lookups after the TTL = %d, want 2", r.calls)
	}
}

func TestSignedNameEmptyAnswerIsCachedErrorIsNot(t *testing.T) {
	r := newNameRig()
	c, e := uuid.New(), uuid.New()
	r.name = ""
	r.n.get(context.Background(), c, e)
	r.n.get(context.Background(), c, e)
	if r.calls != 1 {
		t.Errorf("an empty answer was looked up %d times, want 1", r.calls)
	}
	r = newNameRig()
	r.err = errors.New("boom")
	if got := r.n.get(context.Background(), c, e); got != "" {
		t.Errorf("error answered %q", got)
	}
	r.err = nil
	if got := r.n.get(context.Background(), c, e); got != "story_author" || r.calls != 2 {
		t.Errorf("after an error: %q after %d lookups", got, r.calls)
	}
}

func TestSignedNameForgetDropsOneCommenterOnly(t *testing.T) {
	r := newNameRig()
	a, b, e1, e2 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, k := range [][2]uuid.UUID{{a, e1}, {a, e2}, {b, e1}} {
		r.n.get(context.Background(), k[0], k[1])
	}
	r.calls = 0
	r.n.forget(a)
	r.n.get(context.Background(), b, e1)
	if r.calls != 0 {
		t.Error("forget dropped another commenter")
	}
	r.n.get(context.Background(), a, e1)
	r.n.get(context.Background(), a, e2)
	if r.calls != 2 {
		t.Errorf("lookups after forget = %d, want 2 (every entity of the commenter)", r.calls)
	}
}

func TestSignedNameCapResets(t *testing.T) {
	r := newNameRig()
	for range signedNameCap {
		r.n.get(context.Background(), uuid.New(), uuid.New())
	}
	r.n.get(context.Background(), uuid.New(), uuid.New())
	if got := len(r.n.m); got != 1 {
		t.Errorf("entries after the cap = %d, want 1", got)
	}
}

func TestSignedNameNilReceiver(t *testing.T) {
	var n *signedNames
	if n.get(context.Background(), uuid.New(), uuid.New()) != "" {
		t.Error("nil get")
	}
	n.forget(uuid.New())
}
