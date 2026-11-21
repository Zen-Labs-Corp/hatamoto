// Package webconf renders web-server config for a static site published by
// Hatamoto: the web server serves the export folder and passes the paths only
// Hatamoto answers. It is pure: no I/O, no exec; every value comes from saved
// settings and config, and is checked before it reaches a config file.
package webconf

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/statichost"
)

// ErrUnsafe means a value would break out of the config syntax; nothing is generated.
var ErrUnsafe = errors.New("webconf: unsafe value")

// Header is one response header for the static pages.
type Header struct{ Name, Value string }

// Redirect is one rule of the export's _redirects file.
type Redirect struct {
	From, To string
	Status   int
}

// Input is everything the generator needs.
type Input struct {
	Site     string // normalised origin, e.g. https://example.com
	Live     string // normalised live origin; "" = same host
	Root     string // absolute export folder
	Upstream string // host:port Hatamoto listens on
	// MaxBodyMB is the largest request body nginx passes to Hatamoto on any
	// path (public uploads); 0 leaves nginx's 1 MB default.
	MaxBodyMB int
	// AdminPath and AdminBodyMB raise that limit under the admin path only,
	// where the signed-in uploads and imports live (0 = no extra limit).
	AdminPath   string
	AdminBodyMB int
	Proxy       []statichost.ProxyPath
	Headers     []Header
	Redirects   []Redirect
}

// Output is the config for each server and the redirect rules left out.
type Output struct {
	Caddy, Nginx string
	Skipped      []string
}

// Generate validates in and renders both configs. On ErrUnsafe nothing is returned.
func Generate(in Input) (Output, error) {
	if err := in.validate(); err != nil {
		return Output{}, err
	}
	rules, skipped := planRedirects(in.Redirects)
	return Output{Caddy: caddy(in, rules), Nginx: nginx(in, rules), Skipped: skipped}, nil
}

var (
	pathChars  = regexp.MustCompile(`^/[A-Za-z0-9._~/-]*$`)
	hostChars  = regexp.MustCompile(`^[A-Za-z0-9.\-]+$`)
	headerName = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	// unsafeChars cannot appear in a path or word; headerUnsafe cannot appear in a
	// quoted header value (a CSP needs spaces, quotes of the single kind and ';').
	unsafeChars  = "{};#\"'\\$` \t\r\n"
	headerUnsafe = "{}\"\\$`"
)

func hasControl(v string) bool {
	return strings.IndexFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0
}

func unsafeValue(what, v string) error {
	if strings.ContainsAny(v, unsafeChars) || hasControl(v) {
		return fmt.Errorf("%w: %s", ErrUnsafe, what)
	}
	return nil
}

func (in Input) validate() error {
	if _, _, err := splitOrigin(in.Site); err != nil {
		return fmt.Errorf("%w: site address", ErrUnsafe)
	}
	if in.Live != "" {
		if _, _, err := splitOrigin(in.Live); err != nil {
			return fmt.Errorf("%w: live origin", ErrUnsafe)
		}
	}
	if !strings.HasPrefix(in.Root, "/") || unsafeValue("folder", in.Root) != nil {
		return fmt.Errorf("%w: folder", ErrUnsafe)
	}
	host, port, err := net.SplitHostPort(in.Upstream)
	if n, perr := strconv.Atoi(port); err != nil || perr != nil || n < 1 || n > 65535 || !upstreamHost(host) {
		return fmt.Errorf("%w: upstream", ErrUnsafe)
	}
	if in.MaxBodyMB < 0 || in.MaxBodyMB > 4096 || in.AdminBodyMB < 0 || in.AdminBodyMB > 4096 {
		return fmt.Errorf("%w: upload limit", ErrUnsafe)
	}
	for _, p := range in.Proxy {
		if !pathChars.MatchString(p.Path) {
			return fmt.Errorf("%w: proxy path", ErrUnsafe)
		}
	}
	for _, h := range in.Headers {
		if !headerName.MatchString(h.Name) || strings.ContainsAny(h.Value, headerUnsafe) || hasControl(h.Value) {
			return fmt.Errorf("%w: header %q", ErrUnsafe, h.Name)
		}
	}
	return nil
}

// upstreamHost accepts a DNS name or an IPv4/IPv6 literal.
func upstreamHost(h string) bool {
	return hostChars.MatchString(h) || net.ParseIP(h) != nil
}

// splitOrigin returns the scheme and "host[:port]" of a plain origin.
func splitOrigin(o string) (scheme, hostport string, err error) {
	u, err := url.Parse(o)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", "", errors.New("not a plain origin")
	}
	if !hostChars.MatchString(u.Hostname()) {
		return "", "", errors.New("unsupported host")
	}
	if p := u.Port(); p != "" {
		if _, err := strconv.ParseUint(p, 10, 16); err != nil {
			return "", "", err
		}
	}
	return u.Scheme, u.Host, nil
}

// hostOf is the host[:port] of a validated origin.
func hostOf(origin string) string {
	_, hp, _ := splitOrigin(origin)
	return hp
}

// previewName is the query parameter and cookie of a theme preview.
const previewName = "hm_theme_preview"

// hiddenFiles are the files the web server must never serve.
var hiddenFiles = []string{"/_redirects", "/.hatamoto-static.json"}
