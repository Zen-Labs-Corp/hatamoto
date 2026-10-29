package seo

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeRow struct {
	raw []byte
	err error
}

func (r fakeRow) Scan(dst ...any) error {
	if r.err != nil {
		return r.err
	}
	*dst[0].(*[]byte) = r.raw
	return nil
}

type fakeDB struct {
	row     fakeRow
	queries int
	execs   int
	execArg []any
}

func (f *fakeDB) QueryRow(context.Context, string, ...any) pgx.Row { f.queries++; return f.row }
func (f *fakeDB) Exec(_ context.Context, _ string, a ...any) (pgconn.CommandTag, error) {
	f.execs++
	f.execArg = a
	return pgconn.CommandTag{}, nil
}

func TestGetMissingRowIsDefaults(t *testing.T) {
	st := NewStore(&fakeDB{row: fakeRow{err: pgx.ErrNoRows}})
	got, err := st.Get(context.Background())
	if err != nil || !reflect.DeepEqual(got, Defaults()) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestGetCachesAndInvalidates(t *testing.T) {
	db := &fakeDB{row: fakeRow{raw: []byte(`{"separator":"-"}`)}}
	st := NewStore(db)
	now := time.Unix(1000, 0)
	st.now = func() time.Time { return now }
	got, _ := st.Get(context.Background())
	if got.Separator != "-" {
		t.Fatalf("got %+v", got)
	}
	st.Get(context.Background())
	if db.queries != 1 {
		t.Fatalf("queries = %d, want cached", db.queries)
	}
	now = now.Add(31 * time.Second)
	st.Get(context.Background())
	if db.queries != 2 {
		t.Fatalf("queries = %d after TTL", db.queries)
	}
	st.Invalidate()
	st.Get(context.Background())
	if db.queries != 3 {
		t.Fatalf("queries = %d after Invalidate", db.queries)
	}
}

func TestGetServesStaleOnError(t *testing.T) {
	db := &fakeDB{row: fakeRow{raw: []byte(`{"separator":"-"}`)}}
	st := NewStore(db)
	now := time.Unix(1000, 0)
	st.now = func() time.Time { return now }
	st.Get(context.Background())
	db.row = fakeRow{err: errors.New("down")}
	now = now.Add(time.Minute)
	got, err := st.Get(context.Background())
	if err != nil || got.Separator != "-" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestGetErrorWithoutCacheReturnsDefaults(t *testing.T) {
	st := NewStore(&fakeDB{row: fakeRow{err: errors.New("down")}})
	got, err := st.Get(context.Background())
	if err == nil || !reflect.DeepEqual(got, Defaults()) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestSaveValidatesAndInvalidates(t *testing.T) {
	db := &fakeDB{row: fakeRow{raw: []byte(`{}`)}}
	st := NewStore(db)
	st.Get(context.Background())

	bad := Defaults()
	bad.Separator = "~"
	var ve *ValidationError
	if err := st.Save(context.Background(), bad); !errors.As(err, &ve) || db.execs != 0 {
		t.Fatalf("err = %v, execs = %d", err, db.execs)
	}
	if err := st.Save(context.Background(), Defaults()); err != nil || db.execs != 1 {
		t.Fatalf("err = %v, execs = %d", err, db.execs)
	}
	st.Get(context.Background())
	if db.queries != 2 {
		t.Fatalf("queries = %d, want reload after Save", db.queries)
	}
}

func TestMergeReadsStoreNotCache(t *testing.T) {
	db := &fakeDB{row: fakeRow{raw: []byte(`{"home":"%title%"}`)}}
	st := NewStore(db)
	st.Get(context.Background())
	err := st.Merge(context.Background(), func(s *Settings) { s.Variables["a"] = "b" })
	if err != nil {
		t.Fatal(err)
	}
	if db.queries != 2 {
		t.Fatalf("queries = %d, want a fresh read", db.queries)
	}
	saved, _ := db.execArg[0].(string)
	got, _ := Decode([]byte(saved))
	if got.Home != "%title%" || got.Variables["a"] != "b" {
		t.Fatalf("saved %s", saved)
	}
}
