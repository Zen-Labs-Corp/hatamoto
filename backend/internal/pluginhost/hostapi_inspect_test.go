package pluginhost

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"runtime"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
)

type fakeInspector struct{ calls int }

func (*fakeInspector) ArchiveLimit() int64 { return 1 << 20 }
func (f *fakeInspector) Inspect([]byte, string) sdk.InspectResult {
	f.calls++
	return sdk.InspectResult{Problems: []string{}}
}

func inspectAPI(t *testing.T, svc Services, grant ...sdk.Permission) *API {
	t.Helper()
	return inspectAPIHosts(t, svc, nil, grant...)
}

func inspectAPIHosts(t *testing.T, svc Services, hosts []string, grant ...sdk.Permission) *API {
	t.Helper()
	g := NewGrantStore(nil)
	if err := g.Set(context.Background(), "p", grant); err != nil {
		t.Fatal(err)
	}
	m := sdk.Manifest{Name: "p", AllowedHosts: hosts}
	return NewAPI(svc, g, func(string) (sdk.Manifest, bool) { return m, true }, nil)
}

func callInspect(t *testing.T, a *API, args string) sdk.CallResponse {
	t.Helper()
	return callInspectCtx(t, context.Background(), a, args)
}

func callInspectCtx(t *testing.T, ctx context.Context, a *API, args string) sdk.CallResponse {
	t.Helper()
	in, _ := jsonx.MarshalPinned(sdk.CallRequest{Fn: sdk.FnPackagesInspect, Args: jsontext.Value(args)})
	var out sdk.CallResponse
	if err := jsonx.UnmarshalPinned(a.Call(ctx, "p", in), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func wantInspectCode(t *testing.T, out sdk.CallResponse, code string) {
	t.Helper()
	if out.OK || out.Error == nil || out.Error.Code != code {
		t.Fatalf("want %s, got %+v", code, out)
	}
}

const inspectURL = `{"url":"https://127.0.0.1:1/x.hmp"}`

func TestInspectNeedsTheGrant(t *testing.T) {
	a := inspectAPI(t, Services{Fetcher: NewFetcher(), Packages: &fakeInspector{}})
	out := callInspect(t, a, inspectURL)
	wantInspectCode(t, out, sdk.ErrCapabilityDenied)
	if out.Error.Capability != string(sdk.PermPackagesInspect) {
		t.Fatalf("capability = %q", out.Error.Capability)
	}
}

func TestInspectRefusesBadArguments(t *testing.T) {
	fake := &fakeInspector{}
	a := inspectAPI(t, Services{Fetcher: NewFetcher(), Packages: fake}, sdk.PermPackagesInspect)
	for _, args := range []string{`{}`, `{"url":"http://example.com/x.hmp"}`, `{"url":"ftp://example.com/x"}`, `{"url":"https://example.com/x","extra":1}`} {
		wantInspectCode(t, callInspect(t, a, args), sdk.ErrInvalidArgument)
	}
	if fake.calls != 0 {
		t.Fatal("inspector ran for a bad argument")
	}
}

func TestInspectRateLimit(t *testing.T) {
	a := inspectAPI(t, Services{Fetcher: NewFetcher(), Packages: &fakeInspector{}, FetchPerMinute: 2}, sdk.PermPackagesInspect)
	var limited int
	for range 5 {
		if out := callInspect(t, a, inspectURL); !out.OK && out.Error.Code == sdk.ErrRateLimited {
			limited++
		}
	}
	if limited != 3 {
		t.Fatalf("%d of 5 calls were limited, want 3 (burst of 2)", limited)
	}
}

func TestInspectBlocksPrivateAddress(t *testing.T) {
	a := inspectAPI(t, Services{Fetcher: NewFetcher(), Packages: &fakeInspector{}}, sdk.PermPackagesInspect)
	wantInspectCode(t, callInspect(t, a, inspectURL), sdk.ErrBlocked)
}

func TestInspectUnavailableWithoutInspector(t *testing.T) {
	a := inspectAPI(t, Services{Fetcher: NewFetcher()}, sdk.PermPackagesInspect)
	wantInspectCode(t, callInspect(t, a, inspectURL), sdk.ErrUnavailable)
}

func TestInspectRefusesHostOutsideAllowedHosts(t *testing.T) {
	fake := &fakeInspector{}
	a := inspectAPIHosts(t, Services{Fetcher: NewFetcher(), Packages: fake}, []string{"api.example.com"}, sdk.PermPackagesInspect)
	wantInspectCode(t, callInspect(t, a, `{"url":"https://other.example.org/x.hmp"}`), sdk.ErrBlocked)
	if fake.calls != 0 {
		t.Fatal("inspector ran for a host outside allowed_hosts")
	}
}

func TestInspectWildcardNeedsTheFetchAnyGrant(t *testing.T) {
	a := inspectAPIHosts(t, Services{Fetcher: NewFetcher(), Packages: &fakeInspector{}}, []string{"*"}, sdk.PermPackagesInspect)
	wantInspectCode(t, callInspect(t, a, `{"url":"https://other.example.org/x.hmp"}`), sdk.ErrBlocked)
}

func TestInspectFetchPhaseUsesTheRunsHosts(t *testing.T) {
	a := inspectAPIHosts(t, Services{Fetcher: NewFetcher(), Packages: &fakeInspector{}}, []string{"other.example.org"}, sdk.PermPackagesInspect)
	ctx := WithRun(context.Background(), RunCtx{Phase: sdk.RunPhaseFetch, Hosts: []string{"confirmed.example.com"}})
	wantInspectCode(t, callInspectCtx(t, ctx, a, `{"url":"https://other.example.org/x.hmp"}`), sdk.ErrBlocked)
}

func TestReadBodySizesTheBufferOnce(t *testing.T) {
	const size = 8 << 20
	body := make([]byte, size)
	for _, cl := range []int64{size, -1} {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		runtime.ReadMemStats(&before)
		got, err := readBody(bytes.NewReader(body), cl, 64<<20)
		runtime.ReadMemStats(&after)
		if err != nil || len(got) != size {
			t.Fatalf("size %d: err %v, read %d", cl, err, len(got))
		}
		allocated := int64(after.TotalAlloc - before.TotalAlloc)
		budget := int64(size + size/4) // known length: one buffer of about the body
		if cl < 0 {
			budget = 64<<20 + 1<<20 // unknown length: one buffer of the limit
		}
		if allocated > budget {
			t.Fatalf("size %d: allocated %d bytes, budget %d", cl, allocated, budget)
		}
	}
}

func TestInspectBusyDoesNotSpendRateTokens(t *testing.T) {
	a := inspectAPIHosts(t, Services{Fetcher: NewFetcher(), Packages: &fakeInspector{}, FetchPerMinute: 2}, []string{"127.0.0.1"}, sdk.PermPackagesInspect)
	for range cap(inspectSlots) {
		inspectSlots <- struct{}{}
	}
	for range 5 {
		wantInspectCode(t, callInspect(t, a, inspectURL), sdk.ErrRateLimited)
	}
	for range cap(inspectSlots) {
		<-inspectSlots
	}
	// The slots are free again and the busy calls kept their tokens: the next
	// call reaches the address guard instead of the rate limiter.
	wantInspectCode(t, callInspect(t, a, inspectURL), sdk.ErrBlocked)
}
