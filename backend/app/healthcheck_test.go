package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer ok.Close()
	if err := healthcheck(http.DefaultClient, ok.URL); err != nil {
		t.Errorf("200: %v", err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer bad.Close()
	if err := healthcheck(http.DefaultClient, bad.URL); err == nil {
		t.Error("503 passed")
	}
	if err := healthcheck(http.DefaultClient, "http://127.0.0.1:1/health"); err == nil {
		t.Error("refused connection passed")
	}
}
