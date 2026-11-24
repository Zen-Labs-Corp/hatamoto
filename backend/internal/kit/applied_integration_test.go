//go:build integration

package kit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAppliedRoundTripAndUpsert(t *testing.T) {
	pool, _ := scratchDB(t)
	ctx := context.Background()
	s := AppliedStore{Pool: pool}

	if _, err := s.Get(ctx, "blog"); !errors.Is(err, ErrNotApplied) {
		t.Fatalf("Get absent = %v, want ErrNotApplied", err)
	}
	if err := s.Record(ctx, Applied{Name: "blog", Version: "1.0.0", AppliedBy: "admin@example.com", Summary: []byte(`{"pages":3}`)}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "blog")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "1.0.0" || got.AppliedBy != "admin@example.com" || got.AppliedAt.Location() != time.UTC {
		t.Fatalf("round trip: %+v", got)
	}
	if time.Since(got.AppliedAt) > time.Minute {
		t.Fatalf("applied_at not now: %v", got.AppliedAt)
	}
	if string(got.Summary) != `{"pages": 3}` && string(got.Summary) != `{"pages":3}` {
		t.Fatalf("summary: %s", got.Summary)
	}

	if err := s.Record(ctx, Applied{Name: "blog", Version: "1.1.0", AppliedBy: "cli"}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, "blog")
	if got.Version != "1.1.0" || got.AppliedBy != "cli" || string(got.Summary) != `{}` {
		t.Fatalf("upsert: %+v", got)
	}
	all, _ := s.List(ctx)
	if len(all) != 1 {
		t.Fatalf("upsert made %d rows", len(all))
	}
}

func TestAppliedListOrder(t *testing.T) {
	pool, _ := scratchDB(t)
	ctx := context.Background()
	s := AppliedStore{Pool: pool}
	for _, n := range []string{"a", "b", "c"} {
		if err := s.Record(ctx, Applied{Name: n, Version: "1", AppliedBy: "x"}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Re-applying the oldest moves it to the front.
	if err := s.Record(ctx, Applied{Name: "a", Version: "2", AppliedBy: "x"}); err != nil {
		t.Fatal(err)
	}
	all, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names string
	for _, a := range all {
		names += a.Name
	}
	if names != "acb" {
		t.Fatalf("order = %q, want acb", names)
	}
}

func TestAppliedMigrationDown(t *testing.T) {
	pool, prov := scratchDB(t)
	ctx := context.Background()
	// The newest migration by version is applied_kits (202612010900).
	res, err := prov.Down(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source.Version != 202612010900 {
		t.Skipf("newest migration is %d, not applied_kits", res.Source.Version)
	}
	var reg *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('hatamoto_applied_kits')::text`).Scan(&reg); err != nil {
		t.Fatal(err)
	}
	if reg != nil {
		t.Fatalf("table still exists: %s", *reg)
	}
}
