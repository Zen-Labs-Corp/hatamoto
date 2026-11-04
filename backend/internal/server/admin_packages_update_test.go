package server

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/packages"
)

func TestNewPermissions(t *testing.T) {
	for _, tc := range []struct {
		name               string
		offered, installed []string
		want               []string
	}{
		{"none new", []string{"kv"}, []string{"kv", "mail.send"}, nil},
		{"one new", []string{"kv", "site.scripts"}, []string{"kv"}, []string{"site.scripts"}},
		{"duplicates once", []string{"a", "a"}, nil, []string{"a"}},
		{"nothing offered", nil, []string{"kv"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := newPermissions(tc.offered, tc.installed); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("newPermissions = %v, want %v", got, tc.want)
			}
		})
	}
}

// The cached index drives UpdateNew: only a newer registry version reports
// anything, and an unreachable registry reports nothing.
func TestUpdateNewFor(t *testing.T) {
	p := &packageAdmin{cachedAt: time.Now(), cached: &packages.Index{Packages: []packages.Entry{
		{Kind: packages.KindPlugin, Name: "gtm", Version: "0.2.0", Permissions: []string{"kv", "site.scripts"}},
	}}}
	ctx := context.Background()
	if got := p.updateNewFor(ctx, "gtm", "0.1.0", []string{"kv"}); !reflect.DeepEqual(got, []string{"site.scripts"}) {
		t.Fatalf("older install: got %v", got)
	}
	if got := p.updateNewFor(ctx, "gtm", "0.2.0", []string{"kv"}); got != nil {
		t.Fatalf("up to date: got %v", got)
	}
	if got := p.updateNewFor(ctx, "other", "0.1.0", nil); got != nil {
		t.Fatalf("not in registry: got %v", got)
	}
	p.cached, p.cacheErr = nil, packages.ErrRegistryOff
	if got := p.updateNewFor(ctx, "gtm", "0.1.0", nil); got != nil {
		t.Fatalf("registry off: got %v", got)
	}
}

// A failed registry read must not move "last checked": it keeps the time of the
// last good read.
func TestFailedFetchKeepsLastCheckedTime(t *testing.T) {
	good := time.Now().Add(-2 * time.Hour)
	// https is required without AllowInsecure, so this fetch fails before any network use.
	p := &packageAdmin{s: &Server{packages: &packages.Manager{Registry: &packages.Registry{URL: "http://registry.example.test/registry.json"}}}, fetchedAt: good}
	if _, err := p.index(context.Background()); err == nil {
		t.Fatal("expected the fetch to fail")
	}
	if got := p.fetchedTime(); !got.Equal(good) {
		t.Fatalf("fetchedAt = %v after a failed fetch, want %v", got, good)
	}
}

// A package that asks for more than its registry entry lists as reviewed
// cannot be installed when the registry signed it; with no entry, no review
// data, another signer or no registry nothing changes.
func TestUnreviewedProblem(t *testing.T) {
	reg := packages.FirstPartyKeyIDs[0]
	p := &packageAdmin{cachedAt: time.Now(), cached: &packages.Index{Packages: []packages.Entry{
		{Kind: packages.KindPlugin, Name: "gtm", Version: "0.2.0", Permissions: []string{"kv"}},
		{Kind: packages.KindPlugin, Name: "bare", Version: "1.0.0"},
		{Kind: packages.KindPlugin, Name: "none", Version: "1.0.0", Permissions: []string{}},
	}}}
	ctx := context.Background()
	got := p.unreviewedProblem(ctx, "gtm", reg, []string{"kv", "site.scripts"})
	if !strings.Contains(got, "asks for permissions the registry did not review") || !strings.Contains(got, "site.scripts") || strings.Contains(got, "(kv") {
		t.Fatalf("widened: %q", got)
	}
	if got := p.unreviewedProblem(ctx, "gtm", reg, []string{"kv"}); got != "" {
		t.Fatalf("reviewed: %q", got)
	}
	if got := p.unreviewedProblem(ctx, "other", reg, []string{"site.scripts"}); got != "" {
		t.Fatalf("not in the registry: %q", got)
	}
	if got := p.unreviewedProblem(ctx, "bare", reg, []string{"kv"}); got != "" {
		t.Fatalf("entry without a permissions field: %q", got)
	}
	if got := p.unreviewedProblem(ctx, "none", reg, []string{"kv"}); got == "" {
		t.Fatal("an entry that lists no permissions reviewed none")
	}
	for _, signer := range []string{"", "0123456789abcdef"} {
		if got := p.unreviewedProblem(ctx, "gtm", signer, []string{"kv", "site.scripts"}); got != "" {
			t.Fatalf("signer %q is not the registry's: %q", signer, got)
		}
	}
	p.cached, p.cacheErr = nil, packages.ErrRegistryOff
	if got := p.unreviewedProblem(ctx, "gtm", reg, []string{"site.scripts"}); got != "" {
		t.Fatalf("registry off: %q", got)
	}
}
