//go:build integration

package siteid

import (
	"context"
	"errors"
	"os"
	"regexp"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var uuidShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Run with: DATABASE_URL=... go test -tags=integration ./internal/siteid/...
func newIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestGetReadsStableID(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()

	s := New(pool)
	a, err := s.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if a == "" || a != b {
		t.Fatalf("site id unstable or empty: %q vs %q", a, b)
	}
	if !uuidShape.MatchString(a) {
		t.Errorf("site id %q is not a UUID", a)
	}
	// A fresh Store has no cache, so this value comes from the database.
	c, err := New(pool).Get(ctx)
	if err != nil || c != a {
		t.Errorf("second store = %q (%v), want %q", c, err, a)
	}
}

type fakeRow struct{ err error }

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*string)) = "00000000-0000-4000-8000-000000000001"
	return nil
}

type fakeDB struct {
	errs  []error // one per call; exhausted means success
	calls int
}

func (f *fakeDB) QueryRow(context.Context, string, ...any) pgx.Row {
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
