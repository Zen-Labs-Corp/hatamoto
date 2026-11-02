package indexnow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"hatamoto.dev/core/internal/safefetch"
)

// BatchCap is the most URLs one IndexNow request may carry.
const BatchCap = 10000

const requestTimeout = 15 * time.Second

// newClient returns the production HTTP client: safefetch (no private
// addresses) with redirects pinned to the host the request started at.
func newClient() *http.Client {
	c := safefetch.NewClient(safefetch.ClientOptions{Timeout: requestTimeout})
	c.Jar = nil
	prev := c.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
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
	return c
}

type payload struct {
	Host        string   `json:"host"`
	Key         string   `json:"key"`
	KeyLocation string   `json:"keyLocation"`
	URLList     []string `json:"urlList"`
}

// submit POSTs urls (at most BatchCap) to endpoint. keyLocation is the public
// URL of the key file. It returns the HTTP status; the key is never put in an
// error.
func submit(ctx context.Context, c *http.Client, endpoint, host, key, keyLocation string, urls []string) (int, error) {
	body, err := json.Marshal(payload{Host: host, Key: key, KeyLocation: keyLocation, URLList: urls})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, errors.New("indexnow: bad endpoint")
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := c.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, fmt.Errorf("indexnow: request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("indexnow: endpoint answered %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}
