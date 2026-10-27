package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sync/atomic"
	"time"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
	"hatamoto.dev/core/internal/safefetch"
	"hatamoto.dev/core/internal/worker"
)

// DefaultBaseURL is where release channels and artifacts are published.
const DefaultBaseURL = "https://hatamoto.dev/releases"

// Size caps for what the updater downloads.
const (
	MaxManifestSize = 1 << 20
	MaxRecordSize   = 64 << 10
	MaxSigSize      = 4 << 10
)

const (
	manifestTimeout = 30 * time.Second
	downloadLimit   = 30 * time.Minute
	stallLimit      = 2 * time.Minute
)

// ErrStalled means a download delivered no bytes for too long.
var ErrStalled = errors.New("download stalled")

// ErrTooLarge means a response exceeded its size cap.
var ErrTooLarge = errors.New("response too large")

// Fetcher makes the updater's only network requests: plain anonymous GETs. A
// request carries no headers of its own (Go adds User-Agent and
// Accept-Encoding), no query, no cookies and no identifier of the install.
type Fetcher struct {
	base       *url.URL
	manifest   *http.Client // 30 s total
	artifact   *http.Client // no total timeout; see Download
	stall      time.Duration
	totalLimit time.Duration
}

// NewFetcher validates baseURL ("" = DefaultBaseURL). It must be https; plain
// http to a loopback host is accepted only in test builds.
func NewFetcher(baseURL string) (*Fetcher, error) { return newFetcher(baseURL, allowLoopbackHTTP()) }

func newFetcher(baseURL string, allowLoopback bool) (*Fetcher, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("update URL: %w", err)
	}
	switch {
	case u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "":
		return nil, fmt.Errorf("update URL %q must be a plain scheme://host/path", baseURL)
	case u.Scheme == "https":
	case u.Scheme == "http" && allowLoopback && isLoopbackHost(u.Hostname()):
	default:
		return nil, fmt.Errorf("update URL %q must be https", baseURL)
	}
	mk := func() *http.Client {
		c := safefetch.NewClient(safefetch.ClientOptions{Timeout: manifestTimeout, AllowPrivate: allowLoopback})
		c.Jar = nil
		c.CheckRedirect = redirectGuard(c.CheckRedirect)
		return c
	}
	f := &Fetcher{base: u, manifest: mk(), artifact: mk(), stall: stallLimit, totalLimit: downloadLimit}
	f.artifact.Timeout = 0
	return f, nil
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// redirectGuard keeps every hop on the host the request started at, and never
// lets an https request fall back to http.
func redirectGuard(prev func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if prev != nil {
			if err := prev(req, via); err != nil {
				return err
			}
		}
		first := via[0].URL
		if req.URL.Host != first.Host {
			return fmt.Errorf("redirect to another host (%s) refused", req.URL.Hostname())
		}
		if first.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("redirect from https to http refused")
		}
		return nil
	}
}

func (f *Fetcher) url(elem ...string) string {
	return f.base.JoinPath(elem...).String()
}

// Manifest returns the raw signed channel manifest: one GET of
// <base>/channels/<channel>.json.
func (f *Fetcher) Manifest(ctx context.Context, channel string) ([]byte, error) {
	if !slices.Contains(release.Channels, channel) {
		return nil, fmt.Errorf("unknown channel %q", channel)
	}
	return packages.FetchURL(ctx, f.manifest, f.url("channels", channel+".json"), MaxManifestSize)
}

// Record returns the release record and its detached signature for version.
func (f *Fetcher) Record(ctx context.Context, version string) (rec, sig []byte, err error) {
	if !release.ValidVersion(version) {
		return nil, nil, fmt.Errorf("invalid version %q", version)
	}
	if rec, err = packages.FetchURL(ctx, f.manifest, f.url(version, "release.json"), MaxRecordSize); err != nil {
		return nil, nil, err
	}
	if sig, err = packages.FetchURL(ctx, f.manifest, f.url(version, "release.json.sig"), MaxSigSize); err != nil {
		return nil, nil, err
	}
	return rec, sig, nil
}

// Download streams the binary for version and platform to dst, refusing more
// than max bytes. It stops after a stall (no bytes for two minutes) or 30
// minutes in all. The clock is detached from ctx's deadline, since the
// scheduler's job cap is far shorter than a slow download. Cancelling ctx (the
// server shutting down) does stop it. A failed download
// leaves no file behind.
func (f *Fetcher) Download(ctx context.Context, version, platform, dst string, max int64) (err error) {
	if !release.ValidVersion(version) {
		return fmt.Errorf("invalid version %q", version)
	}
	if !slices.Contains(release.Platforms, platform) {
		return fmt.Errorf("unknown platform %q", platform)
	}
	ctx, cancel := detachDeadline(ctx, f.totalLimit)
	defer cancel()
	var stalled atomic.Bool
	timer := time.AfterFunc(f.stall, func() { stalled.Store(true); cancel() })
	defer timer.Stop()

	u := f.url(version, "hatamoto-"+platform)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := f.artifact.Do(req)
	if err != nil {
		if stalled.Load() {
			return ErrStalled
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", f.url(version, "hatamoto-"+platform), resp.Status)
	}
	if resp.ContentLength > max {
		return fmt.Errorf("%w: %d bytes announced, cap is %d", ErrTooLarge, resp.ContentLength, max)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(dst)
		}
	}()
	n, err := io.Copy(out, io.LimitReader(&resetReader{r: resp.Body, t: timer, d: f.stall}, max+1))
	switch {
	case stalled.Load():
		return ErrStalled
	case err != nil:
		return err
	case n > max:
		return fmt.Errorf("%w: more than %d bytes", ErrTooLarge, max)
	}
	return nil
}

// resetReader restarts the stall timer whenever bytes arrive.
type resetReader struct {
	r io.Reader
	t *time.Timer
	d time.Duration
}

func (s *resetReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.t.Reset(s.d)
	}
	return n, err
}

// detachDeadline returns a context with its own timeout that ignores ctx's
// deadline but follows cancellation of the scheduler's uncapped context
// (worker.JobParent): the job cap must not cut a slow download short, while a
// server shutdown must stop it at any time, including after the cap has fired.
// Outside the scheduler it follows ctx's cancellation, not its deadline.
func detachDeadline(ctx context.Context, limit time.Duration) (context.Context, context.CancelFunc) {
	own, cancel := context.WithTimeout(context.WithoutCancel(ctx), limit)
	parent := worker.JobParent(ctx)
	stop := context.AfterFunc(parent, func() {
		if errors.Is(parent.Err(), context.Canceled) {
			cancel()
		}
	})
	return own, func() { stop(); cancel() }
}
