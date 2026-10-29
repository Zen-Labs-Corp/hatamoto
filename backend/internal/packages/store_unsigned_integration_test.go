//go:build integration

package packages

import (
	"context"
	"testing"
	"time"
)

func TestStoreAllowUnsignedUntil(t *testing.T) {
	pool := scratchDB(t)
	st := NewStore(pool)
	ctx := context.Background()

	check := func(label string, wantAllowed bool, wantUntil bool) Config {
		t.Helper()
		c, err := st.Config(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if c.AllowUnsigned != wantAllowed || (c.AllowUnsignedUntil != nil) != wantUntil {
			t.Fatalf("%s: AllowUnsigned=%v until=%v, want allowed=%v until set=%v", label, c.AllowUnsigned, c.AllowUnsignedUntil, wantAllowed, wantUntil)
		}
		return c
	}

	check("default", false, false)

	// A row from before the column existed: flag on, no end time.
	if err := st.SetAllowUnsigned(ctx, true); err != nil {
		t.Fatal(err)
	}
	check("on, no end", true, false)

	future := time.Now().Add(time.Hour).UTC()
	if err := st.SetAllowUnsignedUntil(ctx, true, &future); err != nil {
		t.Fatal(err)
	}
	c := check("future end", true, true)
	if d := c.AllowUnsignedUntil.Sub(future); d < -time.Millisecond || d > time.Millisecond {
		t.Errorf("until = %v, want %v", c.AllowUnsignedUntil, future)
	}

	past := time.Now().Add(-time.Minute).UTC()
	if err := st.SetAllowUnsignedUntil(ctx, true, &past); err != nil {
		t.Fatal(err)
	}
	check("past end", false, true)

	if err := st.SetAllowUnsignedUntil(ctx, true, &future); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAllowUnsignedUntil(ctx, false, &future); err != nil {
		t.Fatal(err)
	}
	check("off clears the end", false, false)
}
