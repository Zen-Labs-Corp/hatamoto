package comment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeRow struct {
	enabled bool
	raw     []byte
	err     error
}

func (r fakeRow) Scan(dst ...any) error {
	if r.err != nil {
		return r.err
	}
	*dst[0].(*bool) = r.enabled
	*dst[1].(*[]byte) = r.raw
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

func TestSettingsNoRowIsDefaults(t *testing.T) {
	t.Parallel()
	got, err := NewSettingsStore(&fakeDB{row: fakeRow{err: pgx.ErrNoRows}}).Get(context.Background())
	if err != nil || got != Defaults() || got.Enabled {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestSettingsStoredKeysOverDefaults(t *testing.T) {
	t.Parallel()
	st := NewSettingsStore(&fakeDB{row: fakeRow{enabled: true, raw: []byte(`{"max_depth":3}`)}})
	got, _ := st.Get(context.Background())
	want := Defaults()
	want.Enabled, want.MaxDepth = true, 3
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestSettingsGarbageReadsAsDefault(t *testing.T) {
	t.Parallel()
	st := NewSettingsStore(&fakeDB{row: fakeRow{raw: []byte(`{"page_size":9999,"sort":"nope"}`)}})
	got, _ := st.Get(context.Background())
	if got.PageSize != 50 || got.Sort != SortNewest {
		t.Fatalf("got %+v", got)
	}
	st = NewSettingsStore(&fakeDB{row: fakeRow{enabled: true, raw: []byte(`not json`)}})
	if got, _ := st.Get(context.Background()); got.MaxDepth != 5 || !got.Enabled {
		t.Fatalf("got %+v", got)
	}
}

func TestSettingsCacheAndInvalidate(t *testing.T) {
	t.Parallel()
	db := &fakeDB{row: fakeRow{raw: []byte(`{}`)}}
	st := NewSettingsStore(db)
	now := time.Unix(1000, 0)
	st.now = func() time.Time { return now }
	st.Get(context.Background())
	st.Get(context.Background())
	if db.queries != 1 {
		t.Fatalf("queries = %d, want cached", db.queries)
	}
	st.Invalidate()
	st.Get(context.Background())
	if db.queries != 2 {
		t.Fatalf("queries = %d after Invalidate, want 2", db.queries)
	}
	now = now.Add(settingsTTL)
	st.Get(context.Background())
	if db.queries != 3 {
		t.Fatalf("queries = %d after TTL, want 3", db.queries)
	}
}

func TestSettingsReadErrorFailsClosed(t *testing.T) {
	t.Parallel()
	boom := errors.New("down")
	got, err := NewSettingsStore(&fakeDB{row: fakeRow{err: boom}}).Get(context.Background())
	if !errors.Is(err, boom) || got.Enabled {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestSettingsSaveValidatesAndInvalidates(t *testing.T) {
	t.Parallel()
	db := &fakeDB{row: fakeRow{raw: []byte(`{}`)}}
	st := NewSettingsStore(db)
	st.Get(context.Background())
	bad := Defaults()
	bad.PageSize = 0
	if err := st.Save(context.Background(), bad, nil); err == nil || db.execArg != nil {
		t.Fatalf("invalid settings saved: %v", err)
	}
	ok := Defaults()
	ok.Enabled = true
	if err := st.Save(context.Background(), ok, nil); err != nil || len(db.execArg) != 3 {
		t.Fatalf("Save = %v, args %v", err, db.execArg)
	}
	st.Get(context.Background())
	if db.queries != 2 {
		t.Fatalf("queries = %d, want reload after Save", db.queries)
	}
}

func TestResolveMode(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	ago := func(days int) *time.Time { v := now.Add(-time.Duration(days)*24*time.Hour - time.Hour); return &v }
	on := Defaults()
	on.Enabled = true
	noClose := on
	noClose.AutoCloseDays = 0
	cases := []struct {
		name        string
		site        Settings
		schema, ent string
		published   *time.Time
		want        Mode
	}{
		{"site off", Defaults(), "", "open", nil, ModeOff},
		{"entity beats schema", on, "open", "hidden", nil, ModeHidden},
		{"schema inherited", on, "closed", "", nil, ModeClosed},
		{"nothing set", on, "", "", nil, ModeOpen},
		{"auto close after 30d", on, "", "", ago(30), ModeClosed},
		{"open within 30d", on, "", "", ago(29), ModeOpen},
		{"auto close disabled", noClose, "", "", ago(400), ModeOpen},
		{"bogus entity mode inherits", on, "closed", "bogus", nil, ModeClosed},
		{"hidden is not auto closed", on, "hidden", "", ago(400), ModeHidden},
	}
	for _, c := range cases {
		if got := ResolveMode(c.site, c.schema, c.ent, c.published, now); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}
