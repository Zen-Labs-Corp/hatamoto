package cdnpurge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type recvd struct {
	mu   sync.Mutex
	hits int
	body string
	hdr  http.Header
}

func (r *recvd) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hits
}

func receiver(t *testing.T, status int) (*httptest.Server, *recvd) {
	t.Helper()
	rc := &recvd{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rc.mu.Lock()
		rc.hits++
		rc.body, rc.hdr = string(b), r.Header.Clone()
		rc.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, rc
}

func TestWebhookSends(t *testing.T) {
	srv, rc := receiver(t, 200)
	w := NewWebhook(WebhookOptions{URL: srv.URL, Token: "t0k", Client: srv.Client()})
	if err := w.Send(context.Background(), Request{URLs: []string{"https://example.com/a"}}); err != nil {
		t.Fatal(err)
	}
	if rc.body != `{"urls":["https://example.com/a"],"all":false}` {
		t.Errorf("body = %s", rc.body)
	}
	if rc.hdr.Get("Content-Type") != "application/json" || rc.hdr.Get("Authorization") != "Bearer t0k" || rc.hdr.Get("User-Agent") != "Hatamoto" {
		t.Errorf("headers = %v", rc.hdr)
	}
}

func TestWebhookNilURLsIsEmptyArray(t *testing.T) {
	srv, rc := receiver(t, 200)
	w := NewWebhook(WebhookOptions{URL: srv.URL, Client: srv.Client()})
	if err := w.Send(context.Background(), Request{All: true}); err != nil {
		t.Fatal(err)
	}
	if rc.body != `{"urls":[],"all":true}` {
		t.Errorf("body = %s", rc.body)
	}
	if rc.hdr.Get("Authorization") != "" {
		t.Error("Authorization sent without a token")
	}
}

func TestWebhookStatuses(t *testing.T) {
	for _, tc := range []struct {
		status    int
		retryable bool
	}{{503, true}, {429, true}, {404, false}, {400, false}} {
		srv, _ := receiver(t, tc.status)
		w := NewWebhook(WebhookOptions{URL: srv.URL, Token: "t0k", Client: srv.Client()})
		err := w.Send(context.Background(), Request{})
		var re Retryable
		if err == nil || errors.As(err, &re) != tc.retryable || strings.Contains(err.Error(), "t0k") {
			t.Errorf("status %d: err = %v", tc.status, err)
		}
	}
}

func TestWebhookDoesNotFollowRedirects(t *testing.T) {
	target, rc := receiver(t, 200)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(redir.Close)
	// Default client with AllowPrivate so the loopback receiver is reachable.
	w := NewWebhook(WebhookOptions{URL: redir.URL, Token: "t0k", AllowPrivate: true})
	err := w.Send(context.Background(), Request{})
	if err == nil || strings.Contains(err.Error(), "t0k") {
		t.Errorf("err = %v", err)
	}
	if rc.count() != 0 {
		t.Error("redirect target was hit")
	}
}

func TestWebhookDefaultClientBlocksPrivate(t *testing.T) {
	srv, rc := receiver(t, 200)
	w := NewWebhook(WebhookOptions{URL: srv.URL, Token: "t0k"})
	err := w.Send(context.Background(), Request{})
	if err == nil || strings.Contains(err.Error(), "t0k") {
		t.Errorf("err = %v", err)
	}
	if rc.count() != 0 {
		t.Error("private address was reached")
	}
}

func TestWebhookAllowPrivate(t *testing.T) {
	srv, rc := receiver(t, 200)
	w := NewWebhook(WebhookOptions{URL: srv.URL, AllowPrivate: true})
	if err := w.Send(context.Background(), Request{URLs: []string{"https://example.com/a"}}); err != nil {
		t.Fatal(err)
	}
	if rc.count() != 1 {
		t.Errorf("hits = %d", rc.count())
	}
}
