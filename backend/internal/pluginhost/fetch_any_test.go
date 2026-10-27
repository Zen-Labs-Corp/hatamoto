package pluginhost

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"hatamoto.dev/sdk"
)

func fetchAPI(t *testing.T, hosts []string, perMin int, grant ...sdk.Permission) *API {
	t.Helper()
	g := NewGrantStore(nil)
	if err := g.Set(context.Background(), "p", append(grant, sdk.PermHTTPFetch)); err != nil {
		t.Fatal(err)
	}
	m := sdk.Manifest{Name: "p", AllowedHosts: hosts}
	return NewAPI(Services{Fetcher: NewFetcherForTests(), FetchPerMinute: perMin}, g, func(string) (sdk.Manifest, bool) { return m, true }, nil)
}

func callFetch(t *testing.T, a *API, url string) sdk.CallResponse {
	t.Helper()
	in, _ := json.Marshal(sdk.CallRequest{Fn: sdk.FnHTTPFetch, Args: json.RawMessage(`{"url":"` + url + `"}`)})
	var out sdk.CallResponse
	if err := json.Unmarshal(a.Call(context.Background(), "p", in), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// "*" in allowed_hosts only works once the owner granted http.fetch.any.
func TestWildcardHostNeedsTheFetchAnyGrant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	hosts := []string{"*:" + port}

	if out := callFetch(t, fetchAPI(t, hosts, 0), srv.URL); out.OK || out.Error.Code != sdk.ErrBlocked {
		t.Fatalf("without http.fetch.any: %+v", out)
	}
	if out := callFetch(t, fetchAPI(t, hosts, 0, sdk.PermHTTPFetchAny), srv.URL); !out.OK {
		t.Fatalf("with http.fetch.any: %+v", out.Error)
	}
}

func TestFetchRateCapPerPlugin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	a := fetchAPI(t, []string{"127.0.0.1:" + port}, 3)
	var limited int
	for range 8 {
		if out := callFetch(t, a, srv.URL); !out.OK && out.Error.Code == sdk.ErrRateLimited {
			limited++
		}
	}
	if limited != 5 {
		t.Fatalf("%d of 8 calls were limited, want 5 (burst of 3)", limited)
	}
}
