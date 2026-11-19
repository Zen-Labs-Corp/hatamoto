//go:build integration

package packages

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRegistrySeenSurvivesRestart(t *testing.T) {
	pool := scratchDB(t)
	ctx := context.Background()
	key := newKey(t)
	fresh := signedAt(t, key, time.Now().Add(-time.Minute))
	old := signedAt(t, key, time.Now().Add(-60*24*time.Hour))
	srv := serveSequence(t, fresh, old)
	newReg := func() *Registry {
		return &Registry{URL: srv.URL, Client: srv.Client(), Trust: trust(t, key), AllowInsecure: true}
	}

	first := newReg()
	NewManager(Options{Pool: pool, Registry: first})
	if first.Seen == nil {
		t.Fatal("NewManager must wire the store when it has a pool")
	}
	if _, err := first.Fetch(ctx); err != nil {
		t.Fatal(err)
	}

	// A new Manager on the same database still knows the newest index time.
	second := newReg()
	NewManager(Options{Pool: pool, Registry: second})
	if _, err := second.Fetch(ctx); !errors.Is(err, ErrIndexRolledBack) {
		t.Fatalf("older copy after restart: err = %v, want ErrIndexRolledBack", err)
	}

	st := NewStore(pool)
	at := time.Now().UTC().Truncate(time.Second)
	if got, err := st.AdvanceIssued(ctx, "https://example.org/unseen.json", at); err != nil || !got.Equal(at) {
		t.Fatalf("unseen URL: %v, %v; want %v", got, err, at)
	}
	if got, err := st.AdvanceIssued(ctx, "https://example.org/unseen.json", at.Add(-time.Hour)); err != nil || !got.Equal(at) {
		t.Fatalf("older time: %v, %v; want the stored %v", got, err, at)
	}
}
