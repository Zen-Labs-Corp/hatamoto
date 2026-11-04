//go:build integration

package changeset

import (
	"errors"
	"testing"
)

func TestGetReadsChangesetAndItems(t *testing.T) {
	f := newFixture(t)
	cs := f.begin("reader")
	f.stage(cs, f.entityItem(f.tag+"-a", f.tag+"-a", "one"))
	f.stage(cs, f.entityItem(f.tag+"-b", f.tag+"-b", "two"))
	before := f.entities()

	h, items, err := f.eng.Get(f.ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	if h.ID != cs || h.Plugin != "reader" || h.State != StateStaged || h.ItemCount != 2 {
		t.Fatalf("header = %+v", h)
	}
	if len(items) != 2 || items[0].Seq >= items[1].Seq || items[0].SourceKey != f.tag+"-a" {
		t.Fatalf("items = %+v", items)
	}
	if items[0].Result != "" || f.entities() != before {
		t.Fatal("Get must not write")
	}

	if _, _, err := f.eng.Get(f.ctx, cs+1000000); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}
