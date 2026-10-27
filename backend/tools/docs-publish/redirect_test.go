package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// redirectTarget counts requests that reach the redirect destination and
// records whether any carried a credential.
func redirectTarget(t *testing.T) (*httptest.Server, *atomic.Int32, *atomic.Bool) {
	t.Helper()
	var hits atomic.Int32
	var leaked atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("X-API-Key") != "" || r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits, &leaked
}

func redirectingSite(t *testing.T, to string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, to, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRESTClientBlocksRedirect(t *testing.T) {
	t.Parallel()
	target, hits, leaked := redirectTarget(t)
	site := redirectingSite(t, target.URL+"/api/v1/entities")
	c := NewClient(Config{URL: site.URL, APIKey: fakeKey})
	_, err := c.ListEntities(context.Background(), fakeSchema, "")
	if err == nil || !strings.Contains(err.Error(), "redirected") {
		t.Fatalf("want a redirect error, got %v", err)
	}
	if hits.Load() != 0 || leaked.Load() {
		t.Fatalf("redirect was followed: hits=%d leaked=%v", hits.Load(), leaked.Load())
	}
}

func TestMCPClientBlocksRedirect(t *testing.T) {
	t.Parallel()
	target, hits, leaked := redirectTarget(t)
	site := redirectingSite(t, target.URL+"/mcp")
	m := NewMCP(Config{URL: site.URL, APIKey: fakeKey})
	err := m.Call(context.Background(), "menus_get", map[string]string{"location": "sidebar"}, nil)
	if err == nil || !strings.Contains(err.Error(), "redirected") {
		t.Fatalf("want a redirect error, got %v", err)
	}
	if hits.Load() != 0 || leaked.Load() {
		t.Fatalf("redirect was followed: hits=%d leaked=%v", hits.Load(), leaked.Load())
	}
}
