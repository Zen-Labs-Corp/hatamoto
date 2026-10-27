package wpimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/core/internal/safefetch"
)

func noSleep(context.Context, time.Duration) error { return nil }

func TestClientRetriesAndHonoursRetryAfter(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch hits.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.WriteHeader(http.StatusBadGateway)
		default:
			fmt.Fprint(w, `{"ok":true}`)
		}
	}))
	defer srv.Close()
	var waits []time.Duration
	c, err := NewClient(ClientOptions{URL: srv.URL, AllowPrivate: true, RatePerSec: 1000, Sleep: func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ OK bool }
	if _, err := c.JSON(context.Background(), "/x", nil, &out); err != nil || !out.OK {
		t.Fatalf("%v %+v", err, out)
	}
	if hits.Load() != 3 || len(waits) != 2 || waits[0] != 2*time.Second {
		t.Errorf("hits=%d waits=%v", hits.Load(), waits)
	}
}

func TestClientDoesNotRetryClientErrorsAndGivesUp(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if strings.HasPrefix(r.URL.Path, "/gone") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c, _ := NewClient(ClientOptions{URL: srv.URL, AllowPrivate: true, RatePerSec: 1000, MaxRetries: 2, Sleep: noSleep})
	_, _, err := c.Do(context.Background(), srv.URL+"/gone", 1<<10)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 404 || hits.Load() != 1 {
		t.Errorf("404: %v hits=%d", err, hits.Load())
	}
	hits.Store(0)
	_, _, err = c.Do(context.Background(), srv.URL+"/down", 1<<10)
	if !errors.As(err, &he) || he.Status != 503 || hits.Load() != 3 {
		t.Errorf("503: %v hits=%d", err, hits.Load())
	}
}

func TestClientIsSSRFGuarded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "secret") }))
	defer srv.Close()
	c, _ := NewClient(ClientOptions{URL: srv.URL, RatePerSec: 1000, Sleep: noSleep})
	_, _, err := c.Do(context.Background(), srv.URL+"/x", 1<<10)
	if !errors.Is(err, safefetch.ErrBlocked) {
		t.Errorf("loopback reachable without AllowPrivate: %v", err)
	}
	if _, err := NewClient(ClientOptions{URL: "ftp://x.test"}); err == nil {
		t.Error("non-http URL accepted")
	}
	if _, err := NewClient(ClientOptions{URL: "https://user:pw@x.test"}); err == nil {
		t.Error("credentials in the URL accepted")
	}
}

func TestClientSendsCredentialsOnlyToTheSite(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { u, p, _ := r.BasicAuth(); got = u + ":" + p }))
	defer srv.Close()
	c, _ := NewClient(ClientOptions{URL: srv.URL, User: "u", AppPassword: "p", AllowPrivate: true, RatePerSec: 1000})
	if _, _, err := c.Do(context.Background(), srv.URL+"/x", 10); err != nil || got != "u:p" {
		t.Fatalf("own host: %q %v", got, err)
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { u, p, _ := r.BasicAuth(); got = u + ":" + p }))
	defer other.Close()
	if _, _, err := c.Do(context.Background(), other.URL+"/x", 10); err != nil || got != ":" {
		t.Errorf("credentials leaked to another host: %q %v", got, err)
	}
}

func TestPagedFollowsTotalPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pn, _ := strconv.Atoi(page)
		n := 100
		if page == "3" {
			n = 5
		}
		w.Header().Set("X-WP-TotalPages", "3")
		items := make([]map[string]any, n)
		for i := range items {
			items[i] = map[string]any{"id": pn*1000 + i}
		}
		_ = json.NewEncoder(w).Encode(items)
	}))
	defer srv.Close()
	c, _ := NewClient(ClientOptions{URL: srv.URL, AllowPrivate: true, RatePerSec: 1000})
	n := 0
	if err := c.Paged(context.Background(), "/wp/v2/posts", nil, 1000, func(json.RawMessage) error { n++; return nil }); err != nil || n != 205 {
		t.Errorf("n=%d err=%v", n, err)
	}
	if err := c.Paged(context.Background(), "/wp/v2/posts", nil, 150, func(json.RawMessage) error { return nil }); err == nil {
		t.Error("item cap not enforced")
	}
}

func TestDownloadEnforcesSizeCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, strings.Repeat("x", 5000)) }))
	defer srv.Close()
	c, _ := NewClient(ClientOptions{URL: srv.URL, AllowPrivate: true, RatePerSec: 1000})
	if _, _, _, err := c.Download(context.Background(), srv.URL+"/f", t.TempDir(), 1000); err == nil {
		t.Error("oversized file accepted")
	}
	path, size, sum, err := c.Download(context.Background(), srv.URL+"/f", t.TempDir(), 10000)
	if err != nil || size != 5000 || len(sum) != 64 || path == "" {
		t.Errorf("download: %v %d %q", err, size, sum)
	}
}

func TestAuditAcceptsOneRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ok", http.StatusMovedPermanently) })
	mux.HandleFunc("/temp", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ok", http.StatusFound) })
	mux.HandleFunc("/chain", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/old", http.StatusMovedPermanently) })
	h := HandlerProber{Handler: mux}
	res := Audit(context.Background(), "https://old.test", []string{
		"https://old.test/ok", "https://old.test/ok", "https://old.test/old", "https://old.test/temp", "https://old.test/chain",
		"https://old.test/missing", "https://old.test/wp-content/uploads/a.jpg", "https://other.test/x",
	}, h)
	if res.Checked != 5 || res.OK != 1 || res.Redirected != 1 || len(res.Failures) != 3 {
		t.Errorf("%+v", res)
	}
}

// pagedServer answers every collection request with the same 100 items, the way
// a cache plugin that ignores ?page= does, and claims the given page count.
func pagedServer(t *testing.T, totalPages string, hits *atomic.Int32, vary bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if totalPages != "" {
			w.Header().Set("X-WP-TotalPages", totalPages)
		}
		base := 0
		if vary {
			base = int(n) * 1000 // pages differ: only the caps can stop the loop
		}
		items := make([]map[string]int, 100)
		for i := range items {
			items[i] = map[string]int{"id": base + i}
		}
		_ = json.NewEncoder(w).Encode(items)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func pagedClient(t *testing.T, url string) *Client {
	t.Helper()
	c, err := NewClient(ClientOptions{URL: url, AllowPrivate: true, RatePerSec: 100000, MaxPages: 50, Sleep: noSleep})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPagedStopsWhenSiteIgnoresPageParameter(t *testing.T) {
	var hits atomic.Int32
	srv := pagedServer(t, "", &hits, false)
	var got int
	err := pagedClient(t, srv.URL).Paged(context.Background(), "/wp/v2/media", nil, 100000, func(json.RawMessage) error { got++; return nil })
	if !errors.Is(err, ErrPagination) {
		t.Fatalf("err = %v, want ErrPagination", err)
	}
	if hits.Load() != 2 || got != 100 {
		t.Errorf("hits=%d items=%d: want to stop at the repeated second page", hits.Load(), got)
	}
}

func TestPagedPageCeilingAndInflatedHeader(t *testing.T) {
	var hits atomic.Int32
	srv := pagedServer(t, "", &hits, true) // endless, distinct pages, no total header
	err := pagedClient(t, srv.URL).Paged(context.Background(), "/x", nil, 100000, func(json.RawMessage) error { return nil })
	if !errors.Is(err, ErrPagination) || hits.Load() != 50 {
		t.Fatalf("err=%v hits=%d, want ErrPagination after 50 pages", err, hits.Load())
	}

	hits.Store(0)
	srv2 := pagedServer(t, "9999999", &hits, true)
	err = pagedClient(t, srv2.URL).Paged(context.Background(), "/x", nil, 100000, func(json.RawMessage) error { return nil })
	if !errors.Is(err, ErrPagination) || hits.Load() != 1 {
		t.Fatalf("inflated header: err=%v hits=%d, want failure on the first page", err, hits.Load())
	}
}

func TestPagedItemCapIsNeverUnlimited(t *testing.T) {
	var hits atomic.Int32
	srv := pagedServer(t, "", &hits, true)
	c := pagedClient(t, srv.URL)
	got := 0
	err := c.Paged(context.Background(), "/x", nil, 250, func(json.RawMessage) error { got++; return nil })
	if err == nil || got != 250 {
		t.Fatalf("err=%v items=%d, want an error after exactly 250", err, got)
	}
	// A cap of zero (the budget is used up) allows no item at all.
	got = 0
	if err := c.Paged(context.Background(), "/x", nil, 0, func(json.RawMessage) error { got++; return nil }); err == nil || got != 0 {
		t.Fatalf("zero cap: err=%v items=%d", err, got)
	}
}

func TestClientByteBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("x", 600))) }))
	defer srv.Close()
	c, _ := NewClient(ClientOptions{URL: srv.URL, AllowPrivate: true, RatePerSec: 1000, MaxTotalBytes: 1000})
	if _, _, err := c.Do(context.Background(), srv.URL+"/a", 1<<10); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Do(context.Background(), srv.URL+"/b", 1<<10); !errors.Is(err, ErrBudget) {
		t.Fatalf("err = %v, want ErrBudget", err)
	}
}

func TestParseSitemapBudgetAndDepth(t *testing.T) {
	if _, locs, err := ParseSitemap([]byte(`<urlset><url><loc>https://x.test/a</loc></url></urlset>`), 0); err != nil || len(locs) != 0 {
		t.Fatalf("limit 0 must read nothing: %v %v", locs, err)
	}
	deep := "<urlset>" + strings.Repeat("<a>", 1000)
	if _, _, err := ParseSitemap([]byte(deep), 10); err == nil {
		t.Fatal("deep sitemap parsed")
	}
}
