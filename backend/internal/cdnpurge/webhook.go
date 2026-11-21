package cdnpurge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/safefetch"
)

const (
	webhookTimeout = 10 * time.Second
	maxReplyBody   = 64 << 10
)

// Retryable marks an error worth another attempt (network, 429, 5xx).
type Retryable struct{ Err error }

func (e Retryable) Error() string { return e.Err.Error() }
func (e Retryable) Unwrap() error { return e.Err }

// WebhookOptions configures NewWebhook.
type WebhookOptions struct {
	URL          string
	Token        string // sent as Authorization: Bearer when set
	AllowPrivate bool   // the URL may be on a private or loopback address
	Client       *http.Client
}

type webhook struct {
	url, token string
	cli        *http.Client
}

// NewWebhook returns a Target that POSTs each Request as JSON to o.URL.
func NewWebhook(o WebhookOptions) Target {
	c := o.Client
	if c == nil {
		c = safefetch.NewClient(safefetch.ClientOptions{AllowPrivate: o.AllowPrivate, Timeout: webhookTimeout})
		c.Jar = nil
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return &webhook{url: o.URL, token: o.Token, cli: c}
}

func (w *webhook) Name() string { return "purge-url" }

func (w *webhook) Send(ctx context.Context, r Request) error {
	urls := r.URLs
	if urls == nil {
		urls = []string{}
	}
	body, err := jsonx.Marshal(struct {
		URLs []string `json:"urls"`
		All  bool     `json:"all"`
	}{urls, r.All})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return errors.New("cdnpurge: bad webhook URL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Hatamoto")
	if w.token != "" {
		req.Header.Set("Authorization", "Bearer "+w.token)
	}
	resp, err := w.cli.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return Retryable{fmt.Errorf("cdnpurge: webhook request failed: %w", err)}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxReplyBody))
	switch s := resp.StatusCode; {
	case s >= 200 && s <= 299:
		return nil
	case s == http.StatusTooManyRequests || s >= 500:
		return Retryable{fmt.Errorf("cdnpurge: webhook answered %d", s)}
	default:
		return fmt.Errorf("cdnpurge: webhook answered %d", s)
	}
}
