package updater

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// firstSeenDB serves one stored first_seen value and keeps the one written.
type firstSeenDB struct {
	stored  []byte
	written []byte
}

type firstSeenRow struct{ stored []byte }

func (r firstSeenRow) Scan(dest ...any) error {
	*(dest[len(dest)-1].(*[]byte)) = r.stored // first_seen is the last column
	return nil
}

func (d *firstSeenDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return firstSeenRow{d.stored}
}

func (d *firstSeenDB) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	d.written = args[len(args)-1].([]byte)
	return pgconn.CommandTag{}, nil
}

// first_seen is a jsonb column; its bytes are version -> RFC 3339 time with
// sorted keys, and an empty record is {}.
func TestFirstSeenBytes(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	db := &firstSeenDB{}
	st := NewStore(db)
	if err := st.SaveStatus(t.Context(), Status{FirstSeen: map[string]time.Time{"v1.3.0": t0.Add(25*time.Hour + 30*time.Minute), "v1.2.3": t0}}); err != nil {
		t.Fatal(err)
	}
	if want := `{"v1.2.3":"2026-11-01T10:00:00Z","v1.3.0":"2026-11-02T11:30:00Z"}`; string(db.written) != want {
		t.Fatalf("first_seen = %s, want %s", db.written, want)
	}
	if err := st.SaveStatus(t.Context(), Status{}); err != nil {
		t.Fatal(err)
	}
	if string(db.written) != `{}` {
		t.Fatalf("empty first_seen = %s, want {}", db.written)
	}
}

func TestFirstSeenReadsStoredBytes(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name, stored string
		want         map[string]time.Time
	}{
		{"two versions", `{"v1.2.3": "2026-11-01T10:00:00Z", "v1.3.0": "2026-11-01T10:00:00Z"}`, map[string]time.Time{"v1.2.3": t0, "v1.3.0": t0}},
		{"empty object", `{}`, nil},
		{"null", `null`, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, status, err := NewStore(&firstSeenDB{stored: []byte(c.stored)}).Load(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(status.FirstSeen) != len(c.want) {
				t.Fatalf("first seen = %v, want %v", status.FirstSeen, c.want)
			}
			for v, at := range c.want {
				if !status.FirstSeen[v].Equal(at) {
					t.Errorf("%s = %v, want %v", v, status.FirstSeen[v], at)
				}
			}
		})
	}
}
