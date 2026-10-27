package agents

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

type fakeResolver struct {
	mu      sync.Mutex
	ptr     map[string][]string
	forward map[string][]string
	ptrErr  error
	fwdErr  error
	calls   int

	remaining []time.Duration // used by deadlineResolver
}

func (f *fakeResolver) LookupAddr(_ context.Context, addr string) ([]string, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.ptrErr != nil {
		return nil, f.ptrErr
	}
	if n, ok := f.ptr[addr]; ok {
		return n, nil
	}
	return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
}

func (f *fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if f.fwdErr != nil {
		return nil, f.fwdErr
	}
	ips, ok := f.forward[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
	}
	var out []net.IPAddr
	for _, s := range ips {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out, nil
}

func TestVerify(t *testing.T) {
	reg := Default()
	store := NewRangeStore()
	store.Set("openai-gptbot", parseFixture(t, "openai.json"))
	store.Set("google", parseFixture(t, "google.json"))
	// anthropic/openai-user/... deliberately never loaded.

	timeout := &net.DNSError{Err: "i/o timeout", IsTimeout: true}
	res := &fakeResolver{
		ptr: map[string][]string{
			"66.249.66.1":  {"crawl-66-249-66-1.googlebot.com."},
			"203.0.113.50": {"crawl-66-249-66-1.googlebot.com."}, // PTR claims Google, forward points elsewhere
			"198.51.100.7": {"evil.example.net."},
			"54.36.1.1":    {"x.crawl.amazonbot.amazon."},
			"34.1.2.3":     {"3.2.1.34.bc.googleusercontent.com."}, // GCE VM default PTR
		},
		forward: map[string][]string{
			"crawl-66-249-66-1.googlebot.com":   {"66.249.66.1"},
			"x.crawl.amazonbot.amazon":          {"54.36.1.1"},
			"3.2.1.34.bc.googleusercontent.com": {"34.1.2.3"}, // forward-confirms
		},
	}
	v := NewVerifier(store, res)
	vNoRanges := NewVerifier(NewRangeStore(), res)
	ctx := context.Background()

	cases := []struct {
		name  string
		agent string
		ip    string
		want  State
	}{
		{"gptbot in OpenAI range", "gptbot", "20.171.206.9", StateVerified},
		{"gptbot random IP", "gptbot", "8.8.8.8", StateSpoofed},
		{"gptbot v4-mapped in range", "gptbot", "::ffff:20.171.206.9", StateVerified},
		{"googlebot UA from non-Google IP", "googlebot", "198.51.100.7", StateSpoofed},
		{"googlebot from GCE VM (FCrDNS-confirmed googleusercontent PTR)", "googlebot", "34.1.2.3", StateSpoofed},
		{"googleother from GCE VM", "googleother", "34.1.2.3", StateSpoofed},
		{"googlebot in Google range", "googlebot", "66.249.64.5", StateVerified},
		{"googlebot range loaded, miss is spoofed even with FCrDNS", "googlebot", "66.249.66.1", StateSpoofed},
		{"googlebot PTR ok, forward mismatch", "googlebot", "203.0.113.50", StateSpoofed},
		{"googlebot no PTR (NXDOMAIN)", "googlebot", "192.0.2.99", StateSpoofed},
		{"amazonbot rdns only", "amazonbot", "54.36.1.1", StateVerified},
		{"amazonbot rdns wrong ip", "amazonbot", "54.36.1.2", StateSpoofed},
		{"own ranges not loaded, no rdns: pending not unverifiable", "claudebot", "160.79.104.1", StatePending},
		{"no methods (mistral)", "mistralai-user", "1.2.3.4", StateUnverifiable},
	}
	for _, c := range cases {
		a := reg.ByID(c.agent)
		if a == nil {
			t.Fatalf("%s: agent %q missing from registry", c.name, c.agent)
		}
		if got := v.Verify(ctx, a, netip.MustParseAddr(c.ip)); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}

	if got := v.Verify(ctx, reg.ByID("gptbot"), netip.Addr{}); got != StateUnverifiable {
		t.Errorf("invalid ip: got %s", got)
	}
	if got := v.Verify(ctx, nil, netip.MustParseAddr("1.1.1.1")); got != StateUnverifiable {
		t.Errorf("nil agent: got %s", got)
	}

	// Transient DNS failure: pending, and not cached.
	res.ptrErr = timeout
	if got := v.Verify(ctx, reg.ByID("amazonbot"), netip.MustParseAddr("54.36.9.9")); got != StatePending {
		t.Errorf("dns timeout: got %s, want pending", got)
	}
	res.ptrErr = nil
	if got := v.Verify(ctx, reg.ByID("amazonbot"), netip.MustParseAddr("54.36.9.9")); got != StateSpoofed {
		t.Errorf("after recovery: got %s, want spoofed (pending must not be cached)", got)
	}
	// Forward lookup timeout is pending too.
	res.fwdErr = timeout
	if got := v.Verify(ctx, reg.ByID("amazonbot"), netip.MustParseAddr("54.36.1.1")); got == StatePending {
		// 54.36.1.1 is cached verified from the table above, so it must stay verified.
		t.Errorf("cached verified result must survive a later DNS outage")
	}
	if got := vNoRanges.Verify(ctx, reg.ByID("googlebot"), netip.MustParseAddr("66.249.66.2")); got != StateSpoofed {
		// No PTR for .2: NXDOMAIN is spoofed regardless of the forward outage.
		t.Errorf("got %s", got)
	}
	// rDNS stays the fallback while the agent's ranges are not loaded.
	res.fwdErr = nil
	if got := vNoRanges.Verify(ctx, reg.ByID("googlebot"), netip.MustParseAddr("66.249.66.1")); got != StateVerified {
		t.Errorf("googlebot via FCrDNS with ranges not loaded: got %s, want verified", got)
	}
	if got := vNoRanges.Verify(ctx, reg.ByID("googlebot"), netip.MustParseAddr("34.1.2.3")); got != StateSpoofed {
		t.Errorf("GCE VM PTR without ranges: got %s, want spoofed", got)
	}
	res.fwdErr = timeout
	res.ptr["66.249.66.3"] = []string{"crawl-3.googlebot.com."}
	if got := vNoRanges.Verify(ctx, reg.ByID("googlebot"), netip.MustParseAddr("66.249.66.3")); got != StatePending {
		t.Errorf("forward timeout: got %s, want pending", got)
	}
}

func TestVerifyCachesRDNS(t *testing.T) {
	res := &fakeResolver{ptr: map[string][]string{"54.36.1.1": {"x.crawl.amazonbot.amazon"}}, forward: map[string][]string{"x.crawl.amazonbot.amazon": {"54.36.1.1"}}}
	v := NewVerifier(NewRangeStore(), res)
	now := time.Now()
	v.now = func() time.Time { return now }
	a := Default().ByID("amazonbot")
	ip := netip.MustParseAddr("54.36.1.1")
	v.Verify(context.Background(), a, ip)
	v.Verify(context.Background(), a, ip)
	if res.calls != 1 {
		t.Errorf("lookups = %d, want 1 (cached)", res.calls)
	}
	now = now.Add(25 * time.Hour)
	v.Verify(context.Background(), a, ip)
	if res.calls != 2 {
		t.Errorf("lookups = %d, want 2 after TTL", res.calls)
	}
}

func TestVerifyCancelledWhileWaitingForDNSSlot(t *testing.T) {
	v := NewVerifier(NewRangeStore(), &fakeResolver{})
	for range dnsConcurrency {
		v.dnsSem <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := v.Verify(ctx, Default().ByID("amazonbot"), netip.MustParseAddr("54.36.1.1")); got != StatePending {
		t.Errorf("got %s, want pending", got)
	}
}

// deadlineResolver records the remaining deadline of each lookup. A shared
// chain budget would leave the forward lookup less than a full timeout.
type deadlineResolver struct{ fakeResolver }

func (d *deadlineResolver) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	d.record(ctx)
	return d.fakeResolver.LookupAddr(ctx, addr)
}

func (d *deadlineResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	d.record(ctx)
	return d.fakeResolver.LookupIPAddr(ctx, host)
}

func (d *deadlineResolver) record(ctx context.Context) {
	dl, _ := ctx.Deadline()
	d.mu.Lock()
	d.remaining = append(d.remaining, time.Until(dl))
	d.mu.Unlock()
	time.Sleep(60 * time.Millisecond)
}

func TestVerifyDNSTimeoutIsPerLookup(t *testing.T) {
	res := &deadlineResolver{
		ptr:     map[string][]string{"54.36.1.1": {"x.crawl.amazonbot.amazon"}},
		forward: map[string][]string{"x.crawl.amazonbot.amazon": {"54.36.1.1"}}}
	v := NewVerifier(NewRangeStore(), res)
	if got := v.Verify(context.Background(), Default().ByID("amazonbot"), netip.MustParseAddr("54.36.1.1")); got != StateVerified {
		t.Fatalf("got %s", got)
	}
	if len(res.remaining) != 2 {
		t.Fatalf("lookups = %d, want 2", len(res.remaining))
	}
	// The forward lookup must get a fresh budget, not what the PTR lookup left.
	if res.remaining[1] < dnsLookupTimeout-30*time.Millisecond {
		t.Errorf("forward lookup budget %v, want ~%v (fresh)", res.remaining[1], dnsLookupTimeout)
	}
}
