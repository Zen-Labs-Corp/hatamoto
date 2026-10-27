package pluginhost

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"hatamoto.dev/core/internal/safefetch"
)

// http.fetch is the only way a plugin reaches the network. Guards, in order:
//
//  1. the URL's host must match the manifest's allowed_hosts;
//  2. the host is resolved HERE and every address is checked against the
//     blocked ranges (loopback, private, link-local incl. cloud metadata,
//     CGNAT, multicast, unspecified, documentation, IPv4-in-IPv6 forms);
//  3. the connection goes to the checked IP literal, never to the name again,
//     so DNS cannot answer differently between the check and the connect
//     (rebinding);
//  4. each redirect hop repeats 1 to 3 and may not leave http/https.
//
// The body, the response and the time are capped.

// Fetch limits.
const (
	maxFetchRequestBytes  = 1 << 20
	maxFetchResponseBytes = 1 << 20
	defaultFetchTimeout   = 10 * time.Second
	maxFetchTimeout       = 30 * time.Second
	maxFetchRedirects     = 5
)

// Lookup resolves a host name to addresses.
type Lookup func(ctx context.Context, host string) ([]netip.Addr, error)

func defaultLookup(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// Fetcher performs guarded outbound requests.
type Fetcher struct {
	lookup       Lookup
	dialer       net.Dialer
	allowPrivate bool // tests only: skip the address ranges so a loopback server is reachable
}

// NewFetcher returns a Fetcher using the system resolver.
func NewFetcher() *Fetcher { return &Fetcher{lookup: defaultLookup} }

// NewFetcherForTests returns a Fetcher that also reaches loopback and private
// addresses, so a test can stand an httptest server in for a remote service.
// The plugin's allowed_hosts still apply. Never use it outside tests.
func NewFetcherForTests() *Fetcher { return &Fetcher{lookup: defaultLookup, allowPrivate: true} }

// FetchRequest is the input of http.fetch.
type FetchRequest struct {
	Method    string            `json:"method,omitempty"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body,omitempty"`
	BodyB64   string            `json:"body_b64,omitempty"`
	TimeoutMS int               `json:"timeout_ms,omitempty"`
}

// FetchResponse is the result of http.fetch.
type FetchResponse struct {
	Status    int               `json:"status"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body,omitempty"`
	BodyB64   string            `json:"body_b64,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
}

// ErrFetchBlocked is wrapped by every policy refusal (allowlist, address, scheme).
var ErrFetchBlocked = safefetch.ErrBlocked

// blockedAddr reports whether a connection to ip must be refused.
func blockedAddr(ip netip.Addr) bool { return safefetch.BlockedAddr(ip) }

// hostAllowed matches host (and port) against the manifest entries: "host",
// "*.suffix" (subdomains only), "host:port", "*" (any host, for plugins that
// call owner-configured URLs, only with the http.fetch.any grant; see
// fnHTTPFetch) and "*:port". Without a port in the entry only 80 and 443 are
// allowed, which holds for "*" too, so a wildcard cannot reach SMTP, Redis or
// other services on odd ports. The address guard still refuses every
// non-public address.
func hostAllowed(allowed []string, host, port string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, entry := range allowed {
		entry = strings.ToLower(strings.TrimSpace(entry))
		ehost, eport := entry, ""
		if h, p, err := net.SplitHostPort(entry); err == nil {
			ehost, eport = h, p
		}
		if eport == "" && port != "80" && port != "443" {
			continue
		}
		if eport != "" && eport != port {
			continue
		}
		if ehost == "*" {
			return true
		}
		if suffix, ok := strings.CutPrefix(ehost, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
		} else if host == ehost {
			return true
		}
	}
	return false
}

// withoutWildcard drops the entries that match any host ("*" and "*:port").
func withoutWildcard(allowed []string) []string {
	out := make([]string, 0, len(allowed))
	for _, e := range allowed {
		host := strings.TrimSpace(e)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host != "*" {
			out = append(out, e)
		}
	}
	return out
}

func blockedErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrFetchBlocked, fmt.Sprintf(format, a...))
}

// checkURL applies the scheme, userinfo and allowlist rules.
func checkURL(u *url.URL, allowed []string) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return blockedErr("scheme %q", u.Scheme)
	}
	if u.User != nil {
		return blockedErr("credentials in the URL")
	}
	host := u.Hostname()
	if host == "" {
		return blockedErr("no host")
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	if !hostAllowed(allowed, host, port) {
		return blockedErr("host %s:%s is not in the plugin's allowed_hosts", host, port)
	}
	return nil
}

// dialContext resolves, checks and dials the IP literal.
func (f *Fetcher) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	d := safefetch.Dialer{Lookup: safefetch.Lookup(f.lookup), AllowPrivate: f.allowPrivate, Net: f.dialer}
	return d.DialContext(ctx, network, addr)
}

// Do runs req for a plugin whose manifest allows the given hosts.
func (f *Fetcher) Do(ctx context.Context, plugin string, allowed []string, req FetchRequest) (*FetchResponse, error) {
	method := strings.ToUpper(req.Method)
	if method == "" {
		method = http.MethodGet
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil, fmt.Errorf("method %q not allowed", method)
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		return nil, fmt.Errorf("bad url: %w", err)
	}
	if err := checkURL(u, allowed); err != nil {
		return nil, err
	}
	body := []byte(req.Body)
	if req.BodyB64 != "" {
		if body, err = base64.StdEncoding.DecodeString(req.BodyB64); err != nil {
			return nil, fmt.Errorf("body_b64: %w", err)
		}
	}
	if len(body) > maxFetchRequestBytes {
		return nil, fmt.Errorf("request body over %d bytes", maxFetchRequestBytes)
	}
	timeout := defaultFetchTimeout
	if req.TimeoutMS > 0 {
		timeout = min(time.Duration(req.TimeoutMS)*time.Millisecond, maxFetchTimeout)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	hreq, err := http.NewRequestWithContext(ctx, method, u.String(), strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	for k, v := range req.Headers {
		switch strings.ToLower(k) {
		case "host", "connection", "content-length", "transfer-encoding", "upgrade", "te", "trailer", "proxy-authorization", "proxy-connection":
			continue
		}
		hreq.Header.Set(k, v)
	}
	if hreq.Header.Get("User-Agent") == "" {
		hreq.Header.Set("User-Agent", "hatamoto-plugin/"+plugin)
	}

	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            f.dialContext,
			ForceAttemptHTTP2:      true,
			TLSHandshakeTimeout:    10 * time.Second,
			ResponseHeaderTimeout:  timeout,
			DisableKeepAlives:      true,
			MaxResponseHeaderBytes: 64 << 10,
		},
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) > maxFetchRedirects {
				return blockedErr("more than %d redirects", maxFetchRedirects)
			}
			return checkURL(r.URL, allowed)
		},
	}
	defer client.CloseIdleConnections()
	resp, err := client.Do(hreq)
	if err != nil {
		if errors.Is(err, ErrFetchBlocked) {
			var ue *url.Error
			if errors.As(err, &ue) {
				return nil, ue.Err
			}
		}
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchResponseBytes+1))
	if err != nil {
		return nil, err
	}
	out := &FetchResponse{Status: resp.StatusCode, Headers: map[string]string{}}
	if len(data) > maxFetchResponseBytes {
		data, out.Truncated = data[:maxFetchResponseBytes], true
	}
	for _, h := range []string{"Content-Type", "Content-Length", "Etag", "Last-Modified", "Location", "Retry-After", "Cache-Control", "Link"} {
		if v := resp.Header.Get(h); v != "" {
			out.Headers[strings.ToLower(h)] = v
		}
	}
	if isText(data) {
		out.Body = string(data)
	} else {
		out.BodyB64 = base64.StdEncoding.EncodeToString(data)
	}
	return out, nil
}

// isText reports whether data can travel as a JSON string unchanged.
func isText(data []byte) bool { return utf8.Valid(data) }
