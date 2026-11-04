package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/safefetch"
)

const (
	compressionCheckTimeout = 10 * time.Second
	// compressionCheckDelay lets the listener come up before the startup check.
	compressionCheckDelay = 3 * time.Second
)

// compressionResult is the outcome of the last public-address check.
type compressionResult struct {
	URL   string
	At    time.Time
	Plain bool   // the public address answered a text page without compression
	Err   string // the check could not run; the result then says nothing
}

// checkCompression asks the site's own public address for its home page, the
// way a browser that accepts zstd and gzip would, and records whether the
// answer came back compressed. It fetches only PUBLIC_BASE_URL (operator
// set), never blocks a request, and a failed check only logs: it can neither
// fail startup nor show a callout. Runs are serialised.
func (s *Server) checkCompression(ctx context.Context) {
	base := s.preloadBase()
	if base == "" || ctx == nil {
		return
	}
	s.compressCheckMu.Lock()
	defer s.compressCheckMu.Unlock()

	res := &compressionResult{URL: base + "/"}
	plain, err := probeCompression(ctx, res.URL)
	res.At = time.Now().UTC()
	switch {
	case ctx.Err() != nil:
		return // shutting down
	case err != nil:
		res.Err = err.Error()
		s.logger.Warn("compression check: could not reach the public address", slog.String("url", res.URL), slog.String("error", res.Err))
	case plain:
		res.Plain = true
		s.logger.Warn("visitors get uncompressed pages: turn on Compress responses in Settings > Performance, or enable compression in your proxy", slog.String("url", res.URL))
	}
	s.compressCheck.Store(res)
}

// probeCompression reports whether url answers 200 text/html, at least
// httpx.CompressMinSize bytes, without a Content-Encoding.
func probeCompression(ctx context.Context, url string) (plain bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, compressionCheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	// Set explicitly: without it the transport adds gzip itself and hides
	// Content-Encoding from us.
	req.Header.Set("Accept-Encoding", "zstd, gzip")
	client := safefetch.NewClient(safefetch.ClientOptions{Timeout: compressionCheckTimeout, AllowPrivate: true})
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
		return false, nil
	}
	if enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))); enc != "" && enc != "identity" {
		return false, nil
	}
	n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, httpx.CompressMinSize))
	if err != nil {
		return false, err
	}
	return n >= httpx.CompressMinSize, nil
}

// startCompressionCheck runs the startup check in the background after a
// short delay, so the listener is up when the check calls its own address.
func (s *Server) startCompressionCheck(ctx context.Context) {
	go func() {
		t := time.NewTimer(compressionCheckDelay)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
			s.checkCompression(ctx)
		}
	}()
}

// CompressionCheck is the last result as the admin shows it.
func (p *pageCacheService) CompressionCheck() admin.CompressionCheck {
	if p.compressionResult == nil {
		return admin.CompressionCheck{}
	}
	r := p.compressionResult()
	if r == nil {
		return admin.CompressionCheck{}
	}
	return admin.CompressionCheck{Checked: true, URL: r.URL, At: r.At, Plain: r.Plain, Err: r.Err}
}
