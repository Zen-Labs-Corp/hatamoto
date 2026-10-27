package agents

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"sort"
	"time"
)

const (
	refreshUserAgent = "hatamoto-agent-verifier"
	refreshTimeout   = 20 * time.Second
	refreshBodyCap   = 4 << 20
)

// HTTPDoer is the subset of *http.Client the refresher needs.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// RefreshFailure is one range key that could not be refreshed.
type RefreshFailure struct {
	Key string
	Err error
}

// RefreshReport summarises one Refresh run. A failure never aborts the other
// keys and never discards the key's previously loaded prefixes.
type RefreshReport struct {
	Refreshed []string
	Failed    []RefreshFailure
}

// Refresher downloads the operators' published IP ranges into a RangeStore.
// It runs from a background job only, never at request time.
type Refresher struct {
	reg     *Registry
	client  HTTPDoer
	store   *RangeStore
	persist RangePersister // optional
	now     func() time.Time
}

// NewRefresher returns a refresher. client nil means an http.Client with a 20s
// timeout; persist nil means in-memory only.
func NewRefresher(reg *Registry, client HTTPDoer, store *RangeStore, persist RangePersister) *Refresher {
	if client == nil {
		client = &http.Client{Timeout: refreshTimeout}
	}
	return &Refresher{reg: reg, client: client, store: store, persist: persist, now: time.Now}
}

// Refresh fetches every range source. For a key, all URLs must succeed and the
// union must be non-empty; otherwise the key keeps its previous prefixes and is
// reported as failed. Cancelling ctx stops between keys.
func (r *Refresher) Refresh(ctx context.Context) RefreshReport {
	var rep RefreshReport
	keys := make([]string, 0, len(r.reg.IPRangeSources))
	for k := range r.reg.IPRangeSources {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if ctx.Err() != nil {
			return rep
		}
		src := r.reg.IPRangeSources[key]
		var all []netip.Prefix
		var err error
		for _, u := range src.URLs {
			var p []netip.Prefix
			if p, err = r.fetch(ctx, u); err != nil {
				err = fmt.Errorf("%s: %w", u, err)
				break
			}
			all = append(all, p...)
		}
		if err == nil && len(all) == 0 {
			err = fmt.Errorf("no urls configured")
		}
		if err != nil {
			rep.Failed = append(rep.Failed, RefreshFailure{Key: key, Err: err})
			continue
		}
		r.store.Set(key, all)
		if r.persist != nil {
			if perr := r.persist.SaveRanges(ctx, key, all, src.URLs, r.now()); perr != nil {
				// Memory is already updated; only restart survival is lost.
				// Reported as failed only, never as both.
				rep.Failed = append(rep.Failed, RefreshFailure{Key: key, Err: perr})
				continue
			}
		}
		rep.Refreshed = append(rep.Refreshed, key)
	}
	return rep
}

func (r *Refresher) fetch(ctx context.Context, url string) ([]netip.Prefix, error) {
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
	return ParsePrefixes(body)
}
