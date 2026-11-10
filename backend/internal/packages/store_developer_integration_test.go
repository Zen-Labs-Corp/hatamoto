//go:build integration

package packages

import (
	"context"
	"testing"
)

func TestStoreDeveloperFields(t *testing.T) {
	pool := scratchDB(t)
	st := NewStore(pool)
	ctx := context.Background()

	c, err := st.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c.AllowDeveloper {
		t.Fatal("allow_developer must be off on a fresh install")
	}
	for _, on := range []bool{true, false} {
		if err := st.SetAllowDeveloper(ctx, on); err != nil {
			t.Fatal(err)
		}
		if c, _ = st.Config(ctx); c.AllowDeveloper != on {
			t.Fatalf("AllowDeveloper = %v, want %v", c.AllowDeveloper, on)
		}
	}

	key := newKey(t)
	raw, err := PackFiles(map[string][]byte{ThemeManifest: []byte(`{"name":"sunrise","version":"1.0.0"}`)}, &key)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(raw, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	const pub = "a2V5LWJ5dGVz"
	if err := st.put(ctx, pool, p, SourceRegistry, true, pub); err != nil {
		t.Fatal(err)
	}
	row, err := st.Get(ctx, KindTheme, "sunrise")
	if err != nil {
		t.Fatal(err)
	}
	if !row.Developer || row.DeveloperKey != pub || row.DisabledReason != "" || !row.Enabled {
		t.Fatalf("round trip: %+v", row)
	}

	if err := st.DisableWithReason(ctx, KindTheme, "sunrise", "key revoked"); err != nil {
		t.Fatal(err)
	}
	row, _ = st.Get(ctx, KindTheme, "sunrise")
	if row.Enabled || row.DisabledReason != "key revoked" || row.Size == 0 {
		t.Fatalf("after disable: %+v", row)
	}
	if err := st.DisableWithReason(ctx, KindTheme, "nope", "x"); err != ErrNotFound {
		t.Fatalf("missing row: %v", err)
	}

	if err := st.SetEnabled(ctx, KindTheme, "sunrise", false); err != nil {
		t.Fatal(err)
	}
	if row, _ = st.Get(ctx, KindTheme, "sunrise"); row.DisabledReason != "key revoked" {
		t.Fatalf("disabling again must keep the reason: %+v", row)
	}
	if err := st.SetEnabled(ctx, KindTheme, "sunrise", true); err != nil {
		t.Fatal(err)
	}
	if row, _ = st.Get(ctx, KindTheme, "sunrise"); !row.Enabled || row.DisabledReason != "" {
		t.Fatalf("enabling must clear the reason: %+v", row)
	}

	// A fresh install of a non-developer package clears the developer fields.
	if err := st.DisableWithReason(ctx, KindTheme, "sunrise", "again"); err != nil {
		t.Fatal(err)
	}
	if err := st.Put(ctx, p, SourceUpload, true); err != nil {
		t.Fatal(err)
	}
	if row, _ = st.Get(ctx, KindTheme, "sunrise"); row.Developer || row.DeveloperKey != "" || row.DisabledReason != "" {
		t.Fatalf("after update: %+v", row)
	}
}
