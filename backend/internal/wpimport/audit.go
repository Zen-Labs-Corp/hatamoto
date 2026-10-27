package wpimport

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

// Prober asks the new site for a path without following redirects.
type Prober interface {
	Probe(ctx context.Context, path string) (status int, location string, err error)
}

// HostedProber is a Prober that knows the host of the site it probes, so that
// a redirect to any other host can be told from one to the new site.
type HostedProber interface {
	Prober
	Hosts() []string
}

// HandlerProber probes an http.Handler in process (tests, and the admin
// wizard, which has the server's handler).
type HandlerProber struct {
	Handler http.Handler
	// Host is the Host header; empty means example.test.
	Host string
}

// Hosts implements HostedProber.
func (p HandlerProber) Hosts() []string {
	if p.Host == "" {
		return []string{"example.test"}
	}
	return []string{p.Host}
}

// Probe implements Prober.
func (p HandlerProber) Probe(ctx context.Context, path string) (int, string, error) {
	host := p.Host
	if host == "" {
		host = "example.test"
	}
	req := httptest.NewRequest(http.MethodGet, "http://"+host+path, nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	p.Handler.ServeHTTP(rec, req)
	return rec.Code, rec.Header().Get("Location"), nil
}

// HTTPProber probes a running site.
type HTTPProber struct {
	Base   string
	Client *http.Client
}

// Hosts implements HostedProber.
func (p HTTPProber) Hosts() []string {
	if u, err := url.Parse(p.Base); err == nil && u.Host != "" {
		return []string{u.Hostname()}
	}
	return nil
}

// Probe implements Prober.
func (p HTTPProber) Probe(ctx context.Context, path string) (int, string, error) {
	c := p.Client
	if c == nil {
		c = &http.Client{}
	}
	noFollow := *c
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.Base, "/")+path, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("User-Agent", "hatamoto-importer-audit/1")
	resp, err := noFollow.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Location"), nil
}

// AuditFailure is an old URL that does not resolve.
type AuditFailure struct {
	URL      string `json:"url"`
	Status   int    `json:"status"`
	Location string `json:"location,omitempty"`
	Reason   string `json:"reason"`
}

// AuditResult is the outcome of checking every old URL.
type AuditResult struct {
	Checked    int            `json:"checked"`
	OK         int            `json:"ok"`         // answered 200 directly
	Redirected int            `json:"redirected"` // answered one 301 to a 200
	Failures   []AuditFailure `json:"failures,omitempty"`
	// Unresolved are old URLs that answer, but with a page that is not the
	// imported content (see Result.URLConflicts).
	Unresolved []AuditFailure `json:"unresolved,omitempty"`
	// Hidden are old author archives that answer 404 on purpose: the person
	// imported for that author is not public yet. Making the person public
	// gives them a page; a re-run then redirects the old URL to it.
	Hidden []AuditFailure `json:"hidden,omitempty"`
}

// hideAuthors moves the 404 failures of old author archives whose person is
// not public (hidden: normPath(old path) -> person name) into Hidden.
func (a *AuditResult) hideAuthors(hidden map[string]string) {
	if a == nil || len(hidden) == 0 {
		return
	}
	kept := a.Failures[:0]
	for _, f := range a.Failures {
		name, ok := hidden[normPath(pathOf(f.URL))]
		if ok && f.Status == 404 {
			f.Reason = "author page hidden until " + name + " is made public (People); then run the import again to redirect it"
			a.Hidden = append(a.Hidden, f)
			continue
		}
		kept = append(kept, f)
	}
	a.Failures = kept
}

// Audit checks that every old URL of the site answers 200, or exactly one
// permanent redirect to a page that answers 200.
func Audit(ctx context.Context, siteURL string, oldURLs []string, p Prober) *AuditResult {
	res := &AuditResult{}
	base, _ := url.Parse(siteURL)
	// A redirect may name the new site's host, or the old one (same domain
	// after the cut-over); any other host is a redirect off the site.
	hosts := map[string]bool{}
	if base != nil {
		hosts[strings.ToLower(base.Hostname())] = true
	}
	if hp, ok := p.(HostedProber); ok {
		for _, h := range hp.Hosts() {
			hosts[strings.ToLower(h)] = true
		}
	}
	seen := map[string]bool{}
	for _, raw := range oldURLs {
		u, err := url.Parse(raw)
		if err != nil || IsWPContent(u.Path) {
			continue
		}
		if u.Host != "" && base != nil && !strings.EqualFold(u.Hostname(), base.Hostname()) {
			continue
		}
		target := u.RequestURI()
		if seen[target] {
			continue
		}
		seen[target] = true
		res.Checked++
		fail := func(status int, loc, reason string) {
			res.Failures = append(res.Failures, AuditFailure{URL: target, Status: status, Location: loc, Reason: reason})
		}
		status, loc, err := p.Probe(ctx, target)
		switch {
		case err != nil:
			fail(0, "", err.Error())
		case status == http.StatusOK:
			res.OK++
		case status == http.StatusMovedPermanently || status == http.StatusPermanentRedirect:
			next, nerr := sameSitePath(loc, hosts)
			if nerr != nil {
				fail(status, loc, nerr.Error())
				continue
			}
			s2, l2, err := p.Probe(ctx, next)
			switch {
			case err != nil:
				fail(status, loc, err.Error())
			case s2 == http.StatusOK:
				res.Redirected++
			default:
				fail(status, loc, fmt.Sprintf("redirect target answers %d (%s)", s2, l2))
			}
		default:
			fail(status, loc, fmt.Sprintf("answers %d", status))
		}
	}
	return res
}

// sameSitePath turns a Location into the path to probe next. A relative
// Location, or one on a known host of the site, is followed on the address of
// the new site; a redirect to another host fails the audit.
func sameSitePath(loc string, hosts map[string]bool) (string, error) {
	u, err := url.Parse(loc)
	if err != nil || loc == "" {
		return "", fmt.Errorf("bad Location %q", loc)
	}
	if u.Host != "" && !hosts[strings.ToLower(u.Hostname())] {
		return "", fmt.Errorf("redirects off the site to %s", u.Hostname())
	}
	return u.RequestURI(), nil
}
