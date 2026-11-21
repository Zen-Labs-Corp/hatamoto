package staticexport

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"hatamoto.dev/core/internal/safefetch"
)

// EnvS3AllowPrivate=1 lets the S3 endpoint be a private, loopback or link-local
// address (a LAN MinIO).
const EnvS3AllowPrivate = "HATAMOTO_STATIC_S3_ALLOW_PRIVATE"

// ErrPrivateEndpoint is returned for an endpoint on a private or link-local address.
var ErrPrivateEndpoint = errors.New("endpoint refused: private or link-local address")

const (
	s3RequestTimeout      = 30 * time.Minute
	s3HeaderTimeout       = 60 * time.Second
	endpointLookupTimeout = 2 * time.Second
)

// AllowPrivateS3 reports whether the operator opted in to a private endpoint.
func AllowPrivateS3(getenv func(string) string) bool {
	return getenv != nil && strings.TrimSpace(getenv(EnvS3AllowPrivate)) == "1"
}

// CheckS3Endpoint refuses an endpoint whose host is an IP literal that
// safefetch blocks, or localhost. It does no DNS.
func CheckS3Endpoint(endpoint string) error {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return nil // Validate reports it
	}
	h := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return ErrPrivateEndpoint
	}
	if ip, err := netip.ParseAddr(h); err == nil && safefetch.BlockedAddr(ip) {
		return ErrPrivateEndpoint
	}
	return nil
}

// ResolveS3Endpoint is CheckS3Endpoint plus a short lookup of a hostname. A
// lookup error is not a refusal: the dial-time guard stays authoritative. A nil
// lookup does no lookup.
func ResolveS3Endpoint(ctx context.Context, endpoint string, lookup safefetch.Lookup) error {
	if err := CheckS3Endpoint(endpoint); err != nil {
		return err
	}
	if lookup == nil {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Hostname() == "" {
		return nil
	}
	if _, err := netip.ParseAddr(u.Hostname()); err == nil {
		return nil // a public literal
	}
	ctx, cancel := context.WithTimeout(ctx, endpointLookupTimeout)
	defer cancel()
	ips, err := lookup(ctx, u.Hostname())
	if err != nil {
		return nil
	}
	for _, ip := range ips {
		if safefetch.BlockedAddr(ip) {
			return ErrPrivateEndpoint
		}
	}
	return nil
}

// s3HTTPClient dials through the safefetch guard, also on every reconnect, so
// DNS rebinding is refused.
func s3HTTPClient(allowPrivate bool, lookup safefetch.Lookup) *http.Client {
	c := safefetch.NewClient(safefetch.ClientOptions{AllowPrivate: allowPrivate, Lookup: lookup, Timeout: s3RequestTimeout})
	if tr, ok := c.Transport.(*http.Transport); ok {
		tr.ResponseHeaderTimeout = s3HeaderTimeout
	}
	return c
}
