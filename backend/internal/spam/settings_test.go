package spam

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
	vals []any
	err  error
}

func (r fakeRow) Scan(dst ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, d := range dst {
		switch p := d.(type) {
		case *string:
			*p = r.vals[i].(string)
		case *int:
			*p = r.vals[i].(int)
		case *[]byte:
			*p, _ = r.vals[i].([]byte)
		}
	}
	return nil
}

type fakeDB struct {
	row     fakeRow
	queries int
	execArg []any
}

func (f *fakeDB) QueryRow(context.Context, string, ...any) pgx.Row { f.queries++; return f.row }
func (f *fakeDB) Exec(_ context.Context, _ string, a ...any) (pgconn.CommandTag, error) {
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
	db := &fakeDB{row: fakeRow{vals: []any{"turnstile", "key", []byte(nil), 5, 1, "a\n b \n", "cyrillic, cjk", "refuse"}}}
	st := NewStore(db)
	now := time.Unix(1000, 0)
	st.now = func() time.Time { return now }
	got, _ := st.Get(context.Background())
	want := Settings{Provider: "turnstile", SiteKey: "key", MinSeconds: 5, MaxLinks: 1,
		BlockedWords: []string{"a", "b"}, BlockedScripts: []string{"cyrillic", "cjk"}, Action: "refuse"}
	if !reflect.DeepEqual(got, want) {
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
	db := &fakeDB{row: fakeRow{vals: []any{"hcaptcha", "k", []byte(nil), 3, 2, "", "", "spam"}}}
	st := NewStore(db)
	now := time.Unix(1000, 0)
	st.now = func() time.Time { return now }
	st.Get(context.Background())
	db.row = fakeRow{err: errors.New("down")}
	now = now.Add(time.Minute)
	got, err := st.Get(context.Background())
	if err != nil || got.Provider != "hcaptcha" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestValidate(t *testing.T) {
	bad := []func(*Settings){
		func(s *Settings) { s.Provider = "x" },
		func(s *Settings) { s.MinSeconds = 61 },
		func(s *Settings) { s.MinSeconds = -1 },
		func(s *Settings) { s.MaxLinks = 51 },
		func(s *Settings) { s.Action = "drop" },
		func(s *Settings) { s.BlockedScripts = []string{"klingon"} },
	}
	for i, m := range bad {
		s := Defaults()
		m(&s)
		if s.Validate() == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
	if err := Defaults().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	db := &fakeDB{}
	s := Defaults()
	s.Action = "x"
	if err := NewStore(db).Save(context.Background(), s); err == nil || db.execArg != nil {
		t.Fatal("invalid settings were saved")
	}
}

func TestSecretRoundTrip(t *testing.T) {
	key := SecretKey("server-secret")
	sealed, err := SealSecret(key, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	s := Defaults()
	if _, ok := s.Secret(key); ok {
		t.Fatal("empty settings reported a secret")
	}
	s.SecretSealed = sealed
	if got, ok := s.Secret(key); !ok || got != "s3cret" {
		t.Fatalf("Secret = %q, %v", got, ok)
	}
	if _, ok := s.Secret(SecretKey("rotated")); ok {
		t.Fatal("rotated key opened the secret")
	}
}
