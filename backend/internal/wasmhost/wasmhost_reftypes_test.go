package wasmhost

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"hatamoto.dev/sdk"
)

// TestReferenceTypesAreOff records what the host does with modules shaped like
// toolchain output when the reference-types feature is not enabled. The
// fixtures live under testdata/reftypes (see the README there); no Rust or
// TinyGo build is involved, so these show the module shapes, not a toolchain.
func TestReferenceTypesAreOff(t *testing.T) {
	cases := []struct {
		file string
		ok   bool
		why  string
	}{
		{"mvp.wasm", true, "one funcref table, call_indirect, bulk memory"},
		{"overlong_index.wasm", true, "call_indirect with padded table and type immediates"},
		{"refs.wasm", false, "ref.func, table.get, table.set"},
		{"externref_table.wasm", false, "a second, externref table"},
		{"two_funcref_tables.wasm", false, "two funcref tables"},
		{"elem_table1.wasm", false, "an element segment for table 1"},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			wasm, err := os.ReadFile(filepath.Join("testdata", "reftypes", c.file))
			if err != nil {
				t.Fatal(err)
			}
			if err := checkTables(wasm); err != nil && c.ok {
				t.Fatalf("checkTables: %v", err)
			}
			h := newHost(t)
			err = h.load(context.Background(), manifest("reftypes", sdk.Limits{}), wasm)
			t.Logf("%s: err=%v", c.why, err)
			if c.ok && err != nil {
				t.Fatalf("load: %v", err)
			}
			if !c.ok && err == nil {
				t.Fatalf("load succeeded for %s", c.why)
			}
		})
	}
}
