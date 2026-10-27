package wpimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"hatamoto.dev/core/internal/safefetch"
)

// ClientOptions configures the HTTP client that reads the old site.
type ClientOptions struct {
	// URL is the site's home URL.
	URL string
	// User and AppPassword are WordPress application-password credentials;
	// without them only public content is read (no drafts, no menus).
	User, AppPassword string
	// AllowPrivate lets the importer reach a site on a private or loopback
	// address (an intranet WordPress, the test fixture). Off by default: the
	// guard that protects plugin http.fetch applies here too.
	AllowPrivate bool
	// RatePerSec limits requests to the old site; 0 means 10.
	RatePerSec float64
	// MaxRetries is the number of retries after the first attempt; 0 means 4.
	MaxRetries int
	// Timeout is per request; 0 means 30s.
	Timeout time.Duration
	// MaxTotalBytes bounds everything read through Do (REST pages, sitemaps)
	// over the life of the client; 0 means DefaultMaxTotalBytes.
	MaxTotalBytes int64
	// MaxPages is the hard ceiling of pages of one collection; 0 means DefaultMaxPages.
	MaxPages int
	// Lookup overrides name resolution (tests).
	Lookup safefetch.Lookup
	// Sleep overrides the backoff wait (tests).
	Sleep func(ctx context.Context, d time.Duration) error
}

// Defaults of the bounds on what a source site can make the importer read.
const (
	DefaultMaxTotalBytes = 2 << 30
	DefaultMaxPages      = 2000
	// DefaultMaxCollection is the item cap of one REST collection (terms,
	// users, media, menu items).
	DefaultMaxCollection = 100_000
)

// ErrBudget is returned when the source has sent more than the byte budget.
var ErrBudget = errors.New("the source sent more data than the import allows")

// ErrPagination is returned when a collection does not end or does not advance.
var ErrPagination = errors.New("pagination of the source does not end")

// Client is a rate-limited, retrying, SSRF-guarded HTTP client for the old site.
type Client struct {
	Base *url.URL
	opts ClientOptions
	http *http.Client
	lim  *rate.Limiter
	// read counts the bytes returned by Do.
	read atomic.Int64
	// routeParam is set when /wp-json/ is not routable (plain permalinks):
	// the REST route then goes in ?rest_route=.
	routeParam bool
}

// HTTPError is a non-2xx answer that is not worth retrying.
type HTTPError struct {
	Status int
	URL    string
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("GET %s: HTTP %d %s", e.URL, e.Status, strings.TrimSpace(e.Body))
}

// NewClient validates the URL and builds the client.
func NewClient(o ClientOptions) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(o.URL), "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("site URL %q must be an http(s) URL", o.URL)
	}
	if u.User != nil {
		return nil, errors.New("site URL must not contain credentials: use --user and --app-password")
	}
	if o.RatePerSec <= 0 {
		o.RatePerSec = 10
	}
	if o.MaxRetries <= 0 {
		o.MaxRetries = 4
	}
	if o.MaxTotalBytes <= 0 {
		o.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if o.MaxPages <= 0 {
		o.MaxPages = DefaultMaxPages
	}
	if o.Sleep == nil {
		o.Sleep = sleepCtx
	}
	return &Client{
		Base: u,
		opts: o,
		http: safefetch.NewClient(safefetch.ClientOptions{AllowPrivate: o.AllowPrivate, Lookup: o.Lookup, Timeout: o.Timeout}),
		lim:  rate.NewLimiter(rate.Limit(o.RatePerSec), max(1, int(o.RatePerSec))),
	}, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Authed reports whether credentials were given.
func (c *Client) Authed() bool { return c.opts.User != "" && c.opts.AppPassword != "" }

// Host is the site's host name without port.
func (c *Client) Host() string { return strings.ToLower(c.Base.Hostname()) }

// restURL builds the URL of a REST route such as "/wp/v2/posts".
func (c *Client) restURL(route string, q url.Values) string {
	if c.routeParam {
		q = cloneValues(q)
		q.Set("rest_route", route)
		return c.Base.String() + "/?" + q.Encode()
	}
	u := c.Base.String() + "/wp-json" + route
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func cloneValues(q url.Values) url.Values {
	out := url.Values{}
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// Do GETs rawURL with the limiter, retries and the size cap, and returns the
// body and headers. Credentials are only sent to the site's own host.
func (c *Client) Do(ctx context.Context, rawURL string, maxBytes int64) ([]byte, http.Header, error) {
	var body []byte
	var hdr http.Header
	err := c.attempt(ctx, rawURL, func(resp *http.Response) error {
		var err error
		hdr = resp.Header
		body, err = io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
		if err == nil && int64(len(body)) > maxBytes {
			return fmt.Errorf("GET %s: response over %d bytes", rawURL, maxBytes)
		}
		if err == nil && c.read.Add(int64(len(body))) > c.opts.MaxTotalBytes {
			return fmt.Errorf("%w (over %d MB)", ErrBudget, c.opts.MaxTotalBytes>>20)
		}
		return err
	})
	return body, hdr, err
}

// attempt runs one logical request with retries on network errors, 429 and 5xx.
func (c *Client) attempt(ctx context.Context, rawURL string, read func(*http.Response) error) error {
	var last error
	for try := 0; try <= c.opts.MaxRetries; try++ {
		if try > 0 {
			wait := backoff(try, last)
			if err := c.opts.Sleep(ctx, wait); err != nil {
				return err
			}
		}
		if err := c.lim.Wait(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "hatamoto-importer/1")
		req.Header.Set("Accept", "application/json, text/xml, */*;q=0.5")
		if c.Authed() {
			if u, perr := url.Parse(rawURL); perr == nil && strings.EqualFold(u.Host, c.Base.Host) && u.Scheme == c.Base.Scheme {
				req.SetBasicAuth(c.opts.User, c.opts.AppPassword)
			}
		}
		resp, err := c.http.Do(req)
		if err != nil {
			if errors.Is(err, safefetch.ErrBlocked) || ctx.Err() != nil {
				return err
			}
			last = err
			continue
		}
		func() {
			defer resp.Body.Close()
			switch {
			case resp.StatusCode >= 200 && resp.StatusCode < 300:
				last = read(resp)
				if last == nil {
					return
				}
				last = &permanent{last}
			case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
				snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
				last = &retryable{status: resp.StatusCode, after: retryAfter(resp), err: &HTTPError{resp.StatusCode, rawURL, string(snippet)}}
			default:
				snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
				last = &permanent{&HTTPError{resp.StatusCode, rawURL, string(snippet)}}
			}
		}()
		if last == nil {
			return nil
		}
		var p *permanent
		if errors.As(last, &p) {
			return p.err
		}
	}
	var r *retryable
	if errors.As(last, &r) {
		return r.err
	}
	return last
}

type permanent struct{ err error }

func (p *permanent) Error() string { return p.err.Error() }

type retryable struct {
	status int
	after  time.Duration
	err    error
}

func (r *retryable) Error() string { return r.err.Error() }

func retryAfter(resp *http.Response) time.Duration {
	v := resp.Header.Get("Retry-After")
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return min(time.Duration(n)*time.Second, 30*time.Second)
	}
	return 0
}

// backoff is 0.5s, 1s, 2s, ... with jitter, or the server's Retry-After.
func backoff(try int, last error) time.Duration {
	var r *retryable
	if errors.As(last, &r) && r.after > 0 {
		return r.after
	}
	d := 500 * time.Millisecond << (try - 1)
	return min(d, 15*time.Second) + time.Duration(rand.Int64N(int64(250*time.Millisecond)))
}

// JSON GETs a REST route and decodes the answer.
func (c *Client) JSON(ctx context.Context, route string, q url.Values, out any) (http.Header, error) {
	body, hdr, err := c.Do(ctx, c.restURL(route, q), 64<<20)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return nil, fmt.Errorf("GET %s: not JSON (%v): %.80q", route, err, body)
	}
	return hdr, nil
}

// Paged reads every page of a REST collection (100 per page) and calls fn for
// each element. fn gets the raw element so callers decode what they need.
// max is the number of items allowed (never "unlimited"): an item past it is an
// error. A collection that claims too many pages, or returns a page equal to
// the one before (a cache that ignores ?page=), fails with ErrPagination.
func (c *Client) Paged(ctx context.Context, route string, q url.Values, max int, fn func(json.RawMessage) error) error {
	q = cloneValues(q)
	q.Set("per_page", "100")
	max = maxInt(max, 0)
	maxPages := min(c.opts.MaxPages, max/100+2)
	seen := 0
	var prev [sha256.Size]byte
	for page := 1; ; page++ {
		if page > maxPages {
			return fmt.Errorf("%w: %s has more than %d pages", ErrPagination, route, maxPages-1)
		}
		q.Set("page", strconv.Itoa(page))
		var items []json.RawMessage
		hdr, err := c.JSON(ctx, route, q, &items)
		if err != nil {
			var he *HTTPError
			if page > 1 && errors.As(err, &he) && he.Status == http.StatusBadRequest {
				return nil // WordPress answers 400 for a page past the end
			}
			return err
		}
		total, _ := strconv.Atoi(hdr.Get("X-WP-TotalPages"))
		if total > maxPages {
			return fmt.Errorf("%w: %s claims %d pages, more than the %d allowed", ErrPagination, route, total, maxPages)
		}
		h := sha256.New()
		for _, it := range items {
			h.Write(it)
		}
		var sum [sha256.Size]byte
		h.Sum(sum[:0])
		if page > 1 && len(items) > 0 && sum == prev {
			return fmt.Errorf("%w: page %d of %s repeats page %d (the site ignores ?page=)", ErrPagination, page, route, page-1)
		}
		prev = sum
		for _, it := range items {
			if seen >= max {
				return fmt.Errorf("%s has more than %d items: raise the limit", route, max)
			}
			seen++
			if err := fn(it); err != nil {
				return err
			}
		}
		if len(items) < 100 || (total > 0 && page >= total) {
			return nil
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Download fetches a file into a temp file inside dir, enforcing maxBytes, and
// returns its path, size and sha256. The caller removes the file.
func (c *Client) Download(ctx context.Context, rawURL, dir string, maxBytes int64) (path string, size int64, sum string, err error) {
	err = c.attempt(ctx, rawURL, func(resp *http.Response) error {
		if resp.ContentLength > maxBytes {
			return fmt.Errorf("%s is %d bytes, over the %d byte limit", rawURL, resp.ContentLength, maxBytes)
		}
		f, err := os.CreateTemp(dir, "wpimport-*")
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxBytes+1))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil && n > maxBytes {
			err = fmt.Errorf("%s is over the %d byte limit", rawURL, maxBytes)
		}
		if err != nil {
			os.Remove(f.Name())
			return err
		}
		path, size, sum = f.Name(), n, hex.EncodeToString(h.Sum(nil))
		return nil
	})
	return
}
