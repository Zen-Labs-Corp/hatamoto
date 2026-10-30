package agents

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type cdnDoer struct {
	resp map[string]func() (*http.Response, error)
}

func (d cdnDoer) Do(r *http.Request) (*http.Response, error) {
	f, ok := d.resp[r.URL.String()]
	if !ok {
		return nil, errors.New("unexpected url")
	}
	return f()
}

func okBody(s string) func() (*http.Response, error) {
	return func() (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(s))}, nil
	}
}

func status(c int) func() (*http.Response, error) {
	return func() (*http.Response, error) {
		return &http.Response{StatusCode: c, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
}

type memPersist struct {
	saved map[string][]netip.Prefix
	err   error
}

func (m *memPersist) LoadRanges(context.Context) (map[string][]netip.Prefix, error) {
	return m.saved, m.err
}

func (m *memPersist) SaveRanges(_ context.Context, key string, p []netip.Prefix, _ []string, _ time.Time) error {
	if m.saved == nil {
		m.saved = map[string][]netip.Prefix{}
	}
	m.saved[key] = p
	return nil
}

func TestParseTextPrefixes(t *testing.T) {
	got, err := ParseTextPrefixes([]byte("# edge\n173.245.48.0/20\n\n 2400:cb00::/32 \r\n"))
	if err != nil || len(got) != 2 || got[0].String() != "173.245.48.0/20" || got[1].String() != "2400:cb00::/32" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := ParseTextPrefixes([]byte("173.245.48.0/20\nnot-a-cidr\n")); err == nil {
		t.Fatal("junk line must fail")
	}
	if got, err := ParseTextPrefixes(nil); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v, %v", got, err)
	}
}

func TestCDNRefresher(t *testing.T) {
	const v4, v6 = "https://www.cloudflare.com/ips-v4", "https://www.cloudflare.com/ips-v6"
	big := func() (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("#\n", refreshBodyCap)))}, nil
	}
	tests := []struct {
		name string
		resp map[string]func() (*http.Response, error)
		ok   bool
	}{
		{"success", map[string]func() (*http.Response, error){v4: okBody("173.245.48.0/20\n"), v6: okBody("2400:cb00::/32\n")}, true},
		{"one url fails", map[string]func() (*http.Response, error){v4: okBody("173.245.48.0/20\n"), v6: status(500)}, false},
		{"status 500", map[string]func() (*http.Response, error){v4: status(500), v6: status(500)}, false},
		{"empty body", map[string]func() (*http.Response, error){v4: okBody(""), v6: okBody("\n")}, false},
		{"oversize", map[string]func() (*http.Response, error){v4: big, v6: okBody("2400:cb00::/32\n")}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &memPersist{}
			got, err := NewCDNRefresher(cdnDoer{tc.resp}, p).Refresh(context.Background())
			if tc.ok {
				if err != nil || len(got) != 2 || len(p.saved[CDNRangeKey]) != 2 {
					t.Fatalf("got %v, %v, saved %v", got, err, p.saved)
				}
				return
			}
			if err == nil || got != nil || len(p.saved) != 0 {
				t.Fatalf("want error and nothing persisted, got %v, %v, %v", got, err, p.saved)
			}
		})
	}
}

func TestCloudflareFallback(t *testing.T) {
	fb := CloudflareFallback()
	if len(fb) != len(cloudflareFallback) || len(fb) == 0 {
		t.Fatalf("fallback parsed %d of %d", len(fb), len(cloudflareFallback))
	}
	in := func(s string) bool {
		a := netip.MustParseAddr(s)
		for _, p := range fb {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	if !in("172.64.0.1") || !in("2606:4700::1") || in("8.8.8.8") {
		t.Fatal("fallback membership wrong")
	}
	// The embedded list must match the published snapshot in testdata.
	var want []string
	for _, f := range []string{"testdata/cloudflare-ips-v4.txt", "testdata/cloudflare-ips-v6.txt"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		p, err := ParseTextPrefixes(b)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range p {
			want = append(want, x.String())
		}
	}
	for i, x := range fb {
		if x.String() != want[i] {
			t.Fatalf("fallback[%d] = %s, testdata %s", i, x, want[i])
		}
	}
}

func TestLoadCDN(t *testing.T) {
	custom := []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
	if got := LoadCDN(context.Background(), &memPersist{saved: map[string][]netip.Prefix{CDNRangeKey: custom}}); len(got) != 1 || got[0] != custom[0] {
		t.Fatalf("persisted not used: %v", got)
	}
	for name, p := range map[string]RangePersister{"nil": nil, "empty": &memPersist{}, "error": &memPersist{err: errors.New("db")}} {
		if got := LoadCDN(context.Background(), p); len(got) != len(cloudflareFallback) {
			t.Fatalf("%s: want fallback, got %v", name, got)
		}
	}
}

func TestRunCDNRefresh(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	calls, applied := 0, 0
	done := make(chan struct{})
	refresh := func(context.Context) ([]netip.Prefix, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		switch calls {
		case 1:
			return []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}, nil
		case 2:
			return nil, errors.New("boom")
		case 3:
			return nil, nil // empty success must not be applied
		}
		cancel()
		return nil, errors.New("stop")
	}
	go func() {
		RunCDNRefresh(ctx, time.Millisecond, refresh, func(p []netip.Prefix) {
			mu.Lock()
			applied++
			mu.Unlock()
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("did not stop")
	}
	if applied != 1 {
		t.Fatalf("applied %d times, want 1", applied)
	}
}
