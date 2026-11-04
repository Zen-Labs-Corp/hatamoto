package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/httpx"
)

func checkServer(t *testing.T, base string) (*Server, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	cfg := &config.Config{}
	cfg.PublicSite.BaseURL = base
	return &Server{cfg: cfg, logger: slog.New(slog.NewTextHandler(&logs, nil))}, &logs
}

func bigHTML() string {
	return "<html><body>" + strings.Repeat("hello ", 400) + "</body></html>"
}

func TestCheckCompressionPlainWarns(t *testing.T) {
	var gotAE atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAE.Store(r.Header.Get("Accept-Encoding"))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, bigHTML())
	}))
	defer ts.Close()
	s, logs := checkServer(t, ts.URL)
	s.checkCompression(context.Background())

	res := s.compressCheck.Load()
	if res == nil || !res.Plain || res.Err != "" || res.URL != ts.URL+"/" || res.At.IsZero() {
		t.Fatalf("result = %+v", res)
	}
	if gotAE.Load() != "zstd, gzip" {
		t.Fatalf("Accept-Encoding = %v", gotAE.Load())
	}
	if !strings.Contains(logs.String(), "uncompressed pages") || !strings.Contains(logs.String(), ts.URL) {
		t.Fatalf("no warning logged: %s", logs.String())
	}
}

func TestCheckCompressionCompressedAnswerIsFine(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "gzip")
		io.WriteString(w, bigHTML()) // body bytes do not matter: the header decides
	}))
	defer ts.Close()
	s, logs := checkServer(t, ts.URL)
	s.checkCompression(context.Background())
	if res := s.compressCheck.Load(); res == nil || res.Plain || res.Err != "" {
		t.Fatalf("result = %+v", res)
	}
	if logs.Len() != 0 {
		t.Fatalf("unexpected log: %s", logs.String())
	}
}

func TestCheckCompressionSmallOrNonHTMLIsNotPlain(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"small": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "tiny")
		},
		"json": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, strings.Repeat("x", httpx.CompressMinSize+1))
		},
		"404": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, bigHTML())
		},
	} {
		ts := httptest.NewServer(h)
		s, _ := checkServer(t, ts.URL)
		s.checkCompression(context.Background())
		if res := s.compressCheck.Load(); res == nil || res.Plain {
			t.Errorf("%s: result = %+v", name, res)
		}
		ts.Close()
	}
}

func TestCheckCompressionUnreachableRecordsErrNoCallout(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	url := ts.URL
	ts.Close() // nothing listens any more
	s, logs := checkServer(t, url)
	s.checkCompression(context.Background())
	res := s.compressCheck.Load()
	if res == nil || res.Err == "" || res.Plain {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(logs.String(), "could not reach") {
		t.Fatalf("log: %s", logs.String())
	}
	p := &pageCacheService{compressionResult: s.compressCheck.Load}
	if cc := p.CompressionCheck(); !cc.Checked || cc.Err == "" {
		t.Fatalf("service view = %+v", cc)
	}
}

func TestCheckCompressionNoBaseMakesNoCall(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer ts.Close()
	s, _ := checkServer(t, "")
	s.checkCompression(context.Background())
	if s.compressCheck.Load() != nil || calls.Load() != 0 {
		t.Fatal("check ran without a base URL")
	}
	p := &pageCacheService{compressionResult: s.compressCheck.Load}
	if cc := p.CompressionCheck(); cc.Checked {
		t.Fatalf("Checked with no result: %+v", cc)
	}
}

func TestSaveStartsCompressionCheck(t *testing.T) {
	r := newServiceRig(t, false, "")
	done := make(chan struct{}, 1)
	r.svc.lifecycle = func() context.Context { return context.Background() }
	r.svc.runCompressionCheck = func(context.Context) { done <- struct{}{} }
	if err := r.svc.Save(context.Background(), admin.PageCacheSettings{Enabled: true, CompressResponses: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Save did not start a check")
	}
}
