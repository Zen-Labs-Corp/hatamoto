package tags

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeRow struct{ raw []byte }

func (r fakeRow) Scan(dest ...any) error { *(dest[0].(*[]byte)) = r.raw; return nil }

// fakeDB holds the one settings document.
type fakeDB struct{ raw []byte }

func (f *fakeDB) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (f *fakeDB) QueryRow(context.Context, string, ...any) pgx.Row        { return fakeRow{f.raw} }
func (f *fakeDB) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	f.raw = []byte(args[0].(string))
	return pgconn.CommandTag{}, nil
}

func TestDropPackRemovesOnlyThatPluginsKeys(t *testing.T) {
	raw, _ := json.Marshal(Settings{Vendors: map[string]VendorState{
		"acme.stats":  {Enabled: true, ID: "123456"},
		"acme.pixel":  {ID: "999999"},
		"acmex.stats": {Enabled: true, ID: "111111"},
		"ga4":         {Enabled: true, ID: "G-ABC123XYZ9"},
	}})
	db := &fakeDB{raw: raw}
	st := NewStore(db)
	dropped, err := st.DropPack(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 2 || dropped[0] != "acme.pixel" || dropped[1] != "acme.stats" {
		t.Fatalf("dropped = %v", dropped)
	}
	got, err := Decode(db.raw)
	if err != nil || len(got.Vendors) != 2 || !got.Vendors["ga4"].Enabled || got.Vendors["acmex.stats"].ID != "111111" {
		t.Fatalf("stored = %+v, %v", got.Vendors, err)
	}
	before := string(db.raw)
	if dropped, err := st.DropPack(context.Background(), "acme"); err != nil || dropped != nil || string(db.raw) != before {
		t.Fatalf("second drop = %v, %v", dropped, err)
	}
}
