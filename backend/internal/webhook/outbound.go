package webhook

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"hatamoto.dev/core/internal/safefetch"
)

// maxRedirects is how many redirects one attempt follows (as the plugin host did).
const maxRedirects = 5

// portOK reports whether u targets the default http or https port. safefetch
// has no port rule, so the webhook sender adds it: a subscription cannot be
// used to probe other services on a public host.
func portOK(u *url.URL) bool {
	p := u.Port()
	return p == "" || p == "80" || p == "443"
}

// checkHop is the policy for one request URL: scheme and credentials (via
// safefetch), the port, and plain http only when the subscription allows it.
// anyPort is for tests that talk to an httptest server.
func checkHop(u *url.URL, allowHTTP, anyPort bool) error {
	if err := safefetch.CheckScheme(u); err != nil {
		return err
	}
	if u.Scheme == "http" && !allowHTTP {
		return safefetch.Errorf("plain http is not allowed for this webhook")
	}
	if !anyPort && !portOK(u) {
		return safefetch.Errorf("port %s is not allowed (only 80 and 443)", u.Port())
	}
	return nil
}

// newClient returns a client that follows at most maxRedirects redirects and
// applies checkHop to each hop on top of safefetch's dial-time address guard.
// An https to http downgrade hop is refused unless allowHTTP.
func newClient(o Options, allowHTTP bool) *http.Client {
	c := safefetch.NewClient(safefetch.ClientOptions{
		AllowPrivate: o.AllowPrivate,
		Lookup:       o.Lookup,
		Timeout:      2 * MaxTimeoutSeconds * time.Second, // ceiling; attempts bound themselves by context
		MaxRedirects: maxRedirects,
	})
	base := c.CheckRedirect
	c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if err := base(r, via); err != nil {
			return err
		}
		return checkHop(r.URL, allowHTTP, o.AllowPrivate)
	}
	return c
}

// errText is a transport error without the request URL (which may carry a
// query string with credentials); only host-independent detail is kept.
func errText(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}
