package agents

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"os"
	"slices"
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
	const v4ok = "173.245.48.0/20\n103.21.244.0/22\n103.22.200.0/22\n103.31.4.0/22\n141.101.64.0/18\n"
	const v6ok = "2400:cb00::/32\n2606:4700::/32\n2803:f800::/32\n"
	tests := []struct {
		name string
		resp map[string]func() (*http.Response, error)
		ok   bool
	}{
		{"success", map[string]func() (*http.Response, error){v4: okBody(v4ok), v6: okBody(v6ok)}, true},
		{"v4 /0", map[string]func() (*http.Response, error){v4: okBody(v4ok + "0.0.0.0/0\n"), v6: okBody(v6ok)}, false},
		{"v4 broader than /8", map[string]func() (*http.Response, error){v4: okBody(v4ok + "4.0.0.0/7\n"), v6: okBody(v6ok)}, false},
		{"v6 /0", map[string]func() (*http.Response, error){v4: okBody(v4ok), v6: okBody(v6ok + "::/0\n")}, false},
		{"v6 broader than /16", map[string]func() (*http.Response, error){v4: okBody(v4ok), v6: okBody(v6ok + "2000::/3\n")}, false},
		{"too few v4", map[string]func() (*http.Response, error){v4: okBody("173.245.48.0/20\n"), v6: okBody(v6ok)}, false},
		{"too few v6", map[string]func() (*http.Response, error){v4: okBody(v4ok), v6: okBody("2400:cb00::/32\n")}, false},
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
				if err != nil || len(got) != 8 || len(p.saved[CDNRangeKey]) != 8 {
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

func TestRealCloudflareListsPassValidation(t *testing.T) {
	var all []netip.Prefix
	for _, f := range []string{"testdata/cloudflare-ips-v4.txt", "testdata/cloudflare-ips-v6.txt"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		p, err := ParseTextPrefixes(b)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, p...)
	}
	if err := validateCDNRanges(all); err != nil {
		t.Fatalf("published list rejected: %v", err)
	}
	if err := validateCDNRanges(CloudflareFallback()); err != nil {
		t.Fatalf("fallback rejected: %v", err)
	}
}

func TestLoadCDN(t *testing.T) {
	custom := CloudflareFallback()[:0:0]
	for _, s := range []string{"203.0.113.0/24", "198.51.100.0/24", "192.0.2.0/24", "100.64.0.0/16", "10.0.0.0/8", "2001:db8::/32", "2001:db9::/32", "2001:dba::/32"} {
		custom = append(custom, netip.MustParsePrefix(s))
	}
	if got := LoadCDN(context.Background(), &memPersist{saved: map[string][]netip.Prefix{CDNRangeKey: custom}}); len(got) != len(custom) || got[0] != custom[0] {
		t.Fatalf("persisted not used: %v", got)
	}
	for name, bad := range map[string][]netip.Prefix{
		"persisted /0":   append(slices.Clone(custom), netip.MustParsePrefix("0.0.0.0/0")),
		"persisted ::/0": append(slices.Clone(custom), netip.MustParsePrefix("::/0")),
		"persisted tiny": custom[:1],
	} {
		if got := LoadCDN(context.Background(), &memPersist{saved: map[string][]netip.Prefix{CDNRangeKey: bad}}); len(got) != len(cloudflareFallback) {
			t.Fatalf("%s: want fallback, got %v", name, got)
		}
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
