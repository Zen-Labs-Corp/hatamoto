package server

import (
	"context"
	"reflect"
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
