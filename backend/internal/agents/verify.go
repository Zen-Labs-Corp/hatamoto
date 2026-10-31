package agents

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// State is the outcome of verifying one (agent, IP) pair. Its values are the
// hatamoto_agent_hits.verification column values.
type State string

const (
	StateVerified     State = VerificationVerified
	StateSpoofed      State = VerificationSpoofed
	StateUnverifiable State = VerificationUnverifiable
	// StatePending means "could not tell right now" (transient DNS failure):
	// retry later. It is never cached.
	StatePending State = VerificationPending
)

const (
	dnsLookupTimeout = 3 * time.Second
	dnsConcurrency   = 8
	verifyCacheTTL   = 24 * time.Hour
	verifyCacheMax   = 100_000
)

// Resolver is the DNS subset the verifier needs (*net.Resolver satisfies it).
type Resolver interface {
	LookupAddr(ctx context.Context, addr string) ([]string, error)
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type cachedState struct {
	state   State
	expires time.Time
}

// Verifier checks an (agent, IP) pair against the operator's published
// method: IP ranges first, forward-confirmed reverse DNS as the fallback.
type Verifier struct {
	ranges   *RangeStore
	resolver Resolver
	now      func() time.Time

	cacheMu sync.Mutex
	cache   map[string]cachedState
	dnsSem  chan struct{}
}

// NewVerifier returns a verifier. resolver nil means net.DefaultResolver.
func NewVerifier(ranges *RangeStore, resolver Resolver) *Verifier {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return &Verifier{
		ranges:   ranges,
		resolver: resolver,
		now:      time.Now,
		cache:    map[string]cachedState{},
		dnsSem:   make(chan struct{}, dnsConcurrency),
	}
}

// Verify decides the state of a hit claiming to be agent a from ip.
//
//   - no usable IP, or an agent with neither ranges nor rDNS: unverifiable
//   - ranges loaded and ip inside: verified
//   - ranges only, own keys not loaded: pending (never accuse; never terminal)
//   - ranges loaded and ip outside, agent without rDNS: spoofed
//   - ranges loaded and ip outside, agent with rDNS: the ranges are only the
//     fast path (Google, Bing and Apple publish rDNS as the primary method and
//     ranges as a convenience, so a crawler can come from an address the list
//     lacks); forward-confirmed reverse DNS decides, as below
//   - rDNS (alone, or when the ranges are not loaded): PTR must end in an agent suffix and
//     resolve forward to ip; mismatch/NXDOMAIN is spoofed; a transient DNS
//     error is pending.
func (v *Verifier) Verify(ctx context.Context, a *Agent, ip netip.Addr) State {
	if a == nil || !ip.IsValid() {
		return StateUnverifiable
	}
	ip = ip.Unmap().WithZone("")
	if len(a.IPRanges) == 0 && len(a.RDNS) == 0 {
		return StateUnverifiable
	}

	if len(a.IPRanges) > 0 {
		found, loaded := v.ranges.Contains(a.IPRanges, ip)
		if found {
			return StateVerified
		}
		if len(a.RDNS) == 0 {
			if loaded {
				return StateSpoofed // a loaded list without a second method: a miss is spoofed
			}
			// Own ranges not downloaded yet: undecided. The worker turns a pair
			// still pending after pendingMaxAge into unverifiable.
			return StatePending
		}
		// A range miss falls through to FCrDNS against the operator's own
		// suffixes. A cloud VM with a provider-assigned PTR still fails: that
		// name is not under the operator's suffix, and the PTR must resolve
		// forward to the same address, so a forged PTR cannot pass either.
	}
	return v.verifyRDNS(ctx, a, ip)
}

func (v *Verifier) verifyRDNS(ctx context.Context, a *Agent, ip netip.Addr) State {
	key := a.ID + "|" + ip.String()
	now := v.now()
	v.cacheMu.Lock()
	if e, ok := v.cache[key]; ok && now.Before(e.expires) {
		v.cacheMu.Unlock()
		return e.state
	}
	v.cacheMu.Unlock()

	select {
	case v.dnsSem <- struct{}{}:
		defer func() { <-v.dnsSem }()
	case <-ctx.Done():
		return StatePending
	}

	state := StateSpoofed
	lctx, cancel := context.WithTimeout(ctx, dnsLookupTimeout)
	names, err := v.resolver.LookupAddr(lctx, ip.String())
	cancel()
	if err != nil && !isDNSNotFound(err) {
		return StatePending
	}
	for _, name := range names {
		host := strings.ToLower(strings.TrimSuffix(name, "."))
		if !hasAnySuffix(host, a.RDNS) {
			continue
		}
		lctx, cancel := context.WithTimeout(ctx, dnsLookupTimeout) // per lookup, not per chain
		addrs, err := v.resolver.LookupIPAddr(lctx, host)
		cancel()
		if err != nil {
			if !isDNSNotFound(err) {
				return StatePending
			}
			continue
		}
		for _, got := range addrs {
			if x, ok := netip.AddrFromSlice(got.IP); ok && x.Unmap() == ip {
				state = StateVerified
				break
			}
		}
		if state == StateVerified {
			break
		}
	}

	v.cacheMu.Lock()
	if len(v.cache) >= verifyCacheMax {
		clear(v.cache)
	}
	v.cache[key] = cachedState{state: state, expires: now.Add(verifyCacheTTL)}
	v.cacheMu.Unlock()
	return state
}

func hasAnySuffix(host string, suffixes []string) bool {
	for _, s := range suffixes {
		if strings.HasSuffix(host, strings.ToLower(s)) {
			return true
		}
	}
	return false
}

func isDNSNotFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}
