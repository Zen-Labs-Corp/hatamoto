package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/safefetch"
)

// reachProbe remembers whether the site address answered after the last
// export. It never runs on a page load: an export that finishes starts it in
// its own goroutine. The result lives in memory only, so a restart reads as
// "not checked".
type reachProbe struct {
	mu     sync.Mutex
	state  string
	at     time.Time
	client *http.Client
	now    func() time.Time
}

func newReachProbe() *reachProbe {
	// Public addresses only, a short timeout, and a redirect is an answer.
	c := safefetch.NewClient(safefetch.ClientOptions{Timeout: 5 * time.Second})
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &reachProbe{state: admin.HostingCheckUnknown, client: c, now: func() time.Time { return time.Now().UTC() }}
}

// start probes the site address in the background; ctx must outlive the run
// that triggered it (the server's long-lived context).
func (p *reachProbe) start(ctx context.Context, site func(context.Context) string) {
	go p.check(ctx, site(ctx))
}

// check sends HEAD site/: 2xx and 3xx are OK, other answers and network errors
// are failures, and an address the safe client refuses (private or loopback) or
// an unset site is "not checked".
func (p *reachProbe) check(ctx context.Context, site string) {
	state := admin.HostingCheckUnknown
	if site != "" {
		state = p.probe(ctx, site+"/")
	}
	p.mu.Lock()
	p.state, p.at = state, p.now()
	p.mu.Unlock()
}

func (p *reachProbe) probe(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return admin.HostingCheckUnknown
	}
	resp, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, safefetch.ErrBlocked) {
			return admin.HostingCheckUnknown
		}
		return admin.HostingCheckFailed
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return admin.HostingCheckOK
	}
	return admin.HostingCheckFailed
}

// result is the last probe's state and time; "not checked" before the first.
func (p *reachProbe) result() (string, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state, p.at
}
