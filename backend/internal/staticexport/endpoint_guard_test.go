package staticexport

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"hatamoto.dev/core/internal/safefetch"
)

func guardLookup(ips ...string) (safefetch.Lookup, *int) {
	calls := new(int)
	return func(context.Context, string) ([]netip.Addr, error) {
		*calls++
		var out []netip.Addr
		for _, s := range ips {
			out = append(out, netip.MustParseAddr(s))
		}
		return out, nil
	}, calls
}

func TestCheckS3Endpoint(t *testing.T) {
	for _, u := range []string{"https://127.0.0.1", "http://10.0.0.5", "http://169.254.169.254", "https://[::1]",
		"https://[fe80::1]", "http://localhost:9000", "https://100.64.0.1", "https://a.localhost"} {
		if err := CheckS3Endpoint(u); !errors.Is(err, ErrPrivateEndpoint) {
			t.Errorf("%s: err = %v, want refusal", u, err)
		}
	}
	for _, u := range []string{"https://s3.example.com", "https://8.8.8.8"} {
		if err := CheckS3Endpoint(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
}

func TestResolveS3Endpoint(t *testing.T) {
	ctx := context.Background()
	priv, _ := guardLookup("10.0.0.9")
	if err := ResolveS3Endpoint(ctx, "https://s3.example.com", priv); !errors.Is(err, ErrPrivateEndpoint) {
		t.Fatalf("private answer: %v", err)
	}
	failing := safefetch.Lookup(func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("no dns") })
	if err := ResolveS3Endpoint(ctx, "https://s3.example.com", failing); err != nil {
		t.Fatalf("lookup error refused: %v", err)
	}
	pub, calls := guardLookup("8.8.8.8")
	if err := ResolveS3Endpoint(ctx, "https://s3.example.com", pub); err != nil || *calls != 1 {
		t.Fatalf("public answer: %v, %d calls", err, *calls)
	}
	if err := ResolveS3Endpoint(ctx, "https://s3.example.com", nil); err != nil {
		t.Fatalf("nil lookup: %v", err)
	}
	if err := ResolveS3Endpoint(ctx, "http://10.0.0.5", pub); !errors.Is(err, ErrPrivateEndpoint) || *calls != 1 {
		t.Fatalf("literal: %v, %d calls", err, *calls)
	}
}

func TestAllowPrivateS3(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == EnvS3AllowPrivate {
				return v
			}
			return ""
		}
	}
	if AllowPrivateS3(nil) || AllowPrivateS3(env("")) || AllowPrivateS3(env("true")) {
		t.Fatal("opted in without 1")
	}
	if !AllowPrivateS3(env("1")) {
		t.Fatal("1 did not opt in")
	}
}
