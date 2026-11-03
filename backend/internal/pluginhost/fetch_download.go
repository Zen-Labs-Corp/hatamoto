package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// ErrDownloadTooLarge means a download went over its size cap.
var ErrDownloadTooLarge = errors.New("download over the size cap")

// DownloadStatusError is a download answered with a non-2xx status.
type DownloadStatusError struct{ Status int }

func (e *DownloadStatusError) Error() string { return fmt.Sprintf("server answered %d", e.Status) }

// capReader fails with ErrDownloadTooLarge once more than max bytes were read.
type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left < 0 {
		return 0, ErrDownloadTooLarge
	}
	if int64(len(p)) > c.left+1 {
		p = p[:c.left+1]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	if c.left < 0 {
		return n, ErrDownloadTooLarge
	}
	return n, err
}

// Download GETs rawURL under the same guards as Do (allowed hosts, address
// checks, redirects, no proxy) and hands the body, capped at max bytes, to
// sink. A body over the cap makes the reader fail with ErrDownloadTooLarge.
// Unlike Do it does not buffer: it is for files, not API responses.
func (f *Fetcher) Download(ctx context.Context, plugin string, allowed []string, rawURL string, max int64, sink func(io.Reader) error) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("bad url: %w", err)
	}
	if err := checkURL(u, allowed); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "hatamoto-plugin/"+plugin)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            f.dialContext,
			ForceAttemptHTTP2:      true,
			TLSHandshakeTimeout:    10 * time.Second,
			ResponseHeaderTimeout:  defaultFetchTimeout,
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
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.Is(err, ErrFetchBlocked) && errors.As(err, &ue) {
			return ue.Err
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &DownloadStatusError{Status: resp.StatusCode}
	}
	if resp.ContentLength > max {
		return ErrDownloadTooLarge
	}
	return sink(&capReader{r: resp.Body, left: max})
}
