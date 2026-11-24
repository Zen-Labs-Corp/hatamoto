package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"hatamoto.dev/core/internal/kit"
	"hatamoto.dev/core/internal/packages"
)

type fakeAppliedList struct{ rows []kit.Applied }

func (f fakeAppliedList) List(context.Context) ([]kit.Applied, error) { return f.rows, nil }

// fakeKitPackages answers only Registry; the other methods are never reached by List.
type fakeKitPackages struct {
	kit.Packages
	idx *packages.Index
	err error
}

func (f fakeKitPackages) Registry(context.Context) (*packages.Index, error) { return f.idx, f.err }

func TestKitsListRegistryOffStillListsApplied(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	k := &kitsAdmin{
		deps:    kit.Deps{Packages: fakeKitPackages{err: packages.ErrRegistryOff}},
		applied: fakeAppliedList{rows: []kit.Applied{{Name: "community", Version: "1.0.0", AppliedBy: "owner@example.org", AppliedAt: at}}},
	}
	applied, available, err := k.List(context.Background())
	if !errors.Is(err, packages.ErrRegistryOff) {
		t.Fatalf("err = %v, want the registry-off error", err)
	}
	if len(available) != 0 {
		t.Errorf("available = %v, want none", available)
	}
	if len(applied) != 1 || applied[0].Name != "community" || applied[0].Version != "1.0.0" || !applied[0].AppliedAt.Equal(at) {
		t.Errorf("applied = %+v", applied)
	}
}

func TestKitsListMarksAppliedKits(t *testing.T) {
	t.Parallel()

	idx := &packages.Index{Packages: []packages.Entry{
		{Kind: packages.KindKit, Name: "community", Title: "Community site", Version: "1.0.0", Tier: packages.TierFirstParty},
		{Kind: packages.KindKit, Name: "journal", Version: "0.3.0", Tier: packages.TierCommunity},
		{Kind: packages.KindPlugin, Name: "community-plug", Version: "1.0.0", Tier: packages.TierFirstParty},
	}}
	k := &kitsAdmin{deps: kit.Deps{Packages: fakeKitPackages{idx: idx}}, applied: fakeAppliedList{rows: []kit.Applied{{Name: "community", Version: "1.0.0"}}}}
	applied, available, err := k.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(available) != 2 || available[0].Name != "community" || !available[0].Applied || available[1].Applied || available[1].Tier != "community" {
		t.Errorf("available = %+v (plugins must not be listed)", available)
	}
	if len(applied) != 1 || applied[0].Title != "Community site" {
		t.Errorf("applied = %+v", applied)
	}
}

func TestKitTier(t *testing.T) {
	t.Parallel()

	first := packages.FirstPartyKeyIDs[0]
	for _, c := range []struct {
		name, entry, signer, want string
		origin                    packages.Origin
	}{
		{"the index tier wins", packages.TierCommunity, first, packages.TierCommunity, packages.OriginFirstParty},
		{"first-party key", "", first, packages.TierFirstParty, packages.OriginCommunity},
		{"developer grant", "", "abc", packages.TierDeveloper, packages.OriginDeveloper},
		{"another signer", "", "abc", packages.TierCommunity, packages.OriginSelfBuilt},
		{"unsigned", "", "", packages.TierCommunity, packages.OriginSelfBuilt},
	} {
		if got := kitTier(c.entry, c.origin, c.signer); got != c.want {
			t.Errorf("%s: kitTier = %q, want %q", c.name, got, c.want)
		}
	}
}

// deadlinePackages records whether the context of each Registry read had a deadline.
type deadlinePackages struct {
	kit.Packages
	deadlines []time.Duration // 0: none
}

func (f *deadlinePackages) Registry(ctx context.Context) (*packages.Index, error) {
	var left time.Duration
	if d, ok := ctx.Deadline(); ok {
		left = time.Until(d)
	}
	f.deadlines = append(f.deadlines, left)
	return &packages.Index{}, nil
}

func TestKitRegistryTimeoutBoundsOnlyTheIndexRead(t *testing.T) {
	t.Parallel()

	fp := &deadlinePackages{}
	k := &kitsAdmin{deps: kit.Deps{Packages: fp}, applied: fakeAppliedList{}}
	if _, _, err := k.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _ = k.Plan(context.Background(), "community") // the kit is not listed: it stops after the index read
	if len(fp.deadlines) != 2 {
		t.Fatalf("registry reads = %v", fp.deadlines)
	}
	if d := fp.deadlines[0]; d <= 0 || d > kitRegistryTimeout {
		t.Errorf("List read has deadline %v, want up to %v", d, kitRegistryTimeout)
	}
	if d := fp.deadlines[1]; d != 0 {
		t.Errorf("Plan read has deadline %v: downloads must use the registry's own timeouts", d)
	}
}
