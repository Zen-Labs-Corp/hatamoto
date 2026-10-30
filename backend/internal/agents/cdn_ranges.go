package agents

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// CDNRangeKey is the hatamoto_agent_ip_ranges key holding the Cloudflare list.
const CDNRangeKey = "cloudflare"

var cdnRangeURLs = []string{
	"https://www.cloudflare.com/ips-v4",
	"https://www.cloudflare.com/ips-v6",
}

// ParseTextPrefixes parses a plain-text list with one CIDR (or address) per
// line. Blank lines and # comments are skipped; any other unparsable line is
// an error so a changed format never yields a silently partial list.
func ParseTextPrefixes(b []byte) ([]netip.Prefix, error) {
	var out []netip.Prefix
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p, ok := parsePrefix(line)
		if !ok {
			return nil, fmt.Errorf("invalid prefix %q", line)
		}
		out = append(out, p)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// CDNRefresher downloads the Cloudflare edge ranges. It runs from a background
// goroutine only, never at request time.
type CDNRefresher struct {
	client  HTTPDoer
	persist RangePersister // optional
	now     func() time.Time
}

// NewCDNRefresher returns a refresher. client nil means an http.Client with a
// 20s timeout; persist nil means no persistence.
func NewCDNRefresher(client HTTPDoer, persist RangePersister) *CDNRefresher {
	if client == nil {
		client = &http.Client{Timeout: refreshTimeout}
	}
	return &CDNRefresher{client: client, persist: persist, now: time.Now}
}

// Refresh fetches both lists. Both must succeed and the union must be
// non-empty; otherwise it returns an error and nothing is persisted.
func (r *CDNRefresher) Refresh(ctx context.Context) ([]netip.Prefix, error) {
	var all []netip.Prefix
	for _, u := range cdnRangeURLs {
		p, err := r.fetch(ctx, u)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", u, err)
		}
		all = append(all, p...)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("no prefixes published")
	}
	if r.persist != nil {
		if err := r.persist.SaveRanges(ctx, CDNRangeKey, all, cdnRangeURLs, r.now()); err != nil {
			return nil, fmt.Errorf("persist: %w", err)
		}
	}
	return all, nil
}

func (r *CDNRefresher) fetch(ctx context.Context, url string) ([]netip.Prefix, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", refreshUserAgent)
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, refreshBodyCap+1))
	if err != nil {
		return nil, err
	}
	if len(body) > refreshBodyCap {
		return nil, fmt.Errorf("body exceeds %d bytes", refreshBodyCap)
	}
	return ParseTextPrefixes(body)
}

// LoadCDN returns the persisted Cloudflare list, or the embedded fallback when
// nothing usable is stored. The result is never empty.
func LoadCDN(ctx context.Context, persist RangePersister) []netip.Prefix {
	if persist != nil {
		all, err := persist.LoadRanges(ctx)
		if err != nil {
			slog.Warn("cdn ranges: load persisted list failed, using built-in list", "error", err)
		} else if p := all[CDNRangeKey]; len(p) > 0 {
			return p
		}
	}
	return CloudflareFallback()
}

// RunCDNRefresh refreshes once immediately and then every interval until ctx
// ends. A failure is logged and the last good list stays in place; apply is
// only called with a non-empty list.
func RunCDNRefresh(ctx context.Context, interval time.Duration, refresh func(context.Context) ([]netip.Prefix, error), apply func([]netip.Prefix)) {
	run := func() {
		p, err := refresh(ctx)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("cdn ranges: refresh failed, keeping last good list", "error", err)
			}
			return
		}
		if len(p) == 0 {
			return
		}
		apply(p)
	}
	run()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
