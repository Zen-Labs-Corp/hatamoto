package siteid

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

type fakeRow struct{ err error }

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*string)) = "00000000-0000-4000-8000-000000000001"
	return nil
}

type fakeDB struct {
	mu    sync.Mutex
	errs  []error // one per call; exhausted means success
	calls int
}

func (f *fakeDB) QueryRow(context.Context, string, ...any) pgx.Row {
	f.mu.Lock()
	defer f.mu.Unlock()
	var err error
	if f.calls < len(f.errs) {
		err = f.errs[f.calls]
	}
	f.calls++
	return fakeRow{err}
}

func TestGetMissingRowAndErrorsNotCached(t *testing.T) {
	db := &fakeDB{errs: []error{pgx.ErrNoRows}}
	s := New(db)
	if _, err := s.Get(context.Background()); !errors.Is(err, ErrMissing) {
		t.Fatalf("err = %v, want ErrMissing", err)
	}
	id, err := s.Get(context.Background())
	if err != nil || id == "" {
		t.Fatalf("retry = %q (%v), want a value", id, err)
	}
	if db.calls != 2 {
		t.Errorf("calls = %d, want 2 (error must not be cached)", db.calls)
	}
	if _, err := s.Get(context.Background()); err != nil || db.calls != 2 {
		t.Errorf("success must be cached: err=%v calls=%d", err, db.calls)
	}
}

// blockingDB parks QueryRow until its ctx is done, like a hung connection.
type blockingDB struct{ started chan struct{} }

type ctxRow struct{ ctx context.Context }

func (r ctxRow) Scan(...any) error { <-r.ctx.Done(); return r.ctx.Err() }

func (b blockingDB) QueryRow(ctx context.Context, _ string, _ ...any) pgx.Row {
	b.started <- struct{}{}
	return ctxRow{ctx}
}

func TestGetDoesNotSerializeCallersOnSlowQuery(t *testing.T) {
	db := blockingDB{started: make(chan struct{}, 2)}
	s := New(db)
	hung, cancelHung := context.WithCancel(context.Background())
	defer cancelHung()
	go func() { _, _ = s.Get(hung) }()
	<-db.started // first caller is inside the query

	short, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := s.Get(short); done <- err }()
	<-db.started // second caller reached the query too: no lock held across it
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
