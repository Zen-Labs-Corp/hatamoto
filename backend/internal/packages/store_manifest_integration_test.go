//go:build integration

package packages

import (
	"context"
	"testing"
)

// A plugin installed before the intake check existed, with a repeated name,
// still loads from the store.
func TestStoreLoadsInstalledAmbiguousManifest(t *testing.T) {
	pool := scratchDB(t)
	st := NewStore(pool)
	ctx := context.Background()
	p := parse(t, packManifest(t, `{"NAME":"hello","name":"hello","version":"1.0.0","sdk":"^0.7"}`))
	if err := st.Put(ctx, p, SourceUpload, true); err != nil {
		t.Fatal(err)
	}
	got, _, err := st.Load(ctx, KindPlugin, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "hello" || got.Version != "1.0.0" {
		t.Fatalf("loaded %q %q", got.Name, got.Version)
	}
}
