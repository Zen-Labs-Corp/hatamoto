package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCountServerErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   int
	}{
		{"503", http.StatusServiceUnavailable, 1},
		{"500", http.StatusInternalServerError, 1},
		{"200", http.StatusOK, 0},
		{"404", http.StatusNotFound, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := 0
			h := CountServerErrors(func() { n++ })(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
			if n != tc.want {
				t.Fatalf("count = %d, want %d", n, tc.want)
			}
		})
	}
}

func TestCountServerErrorsSilentHandlerIs200(t *testing.T) {
	n := 0
	h := CountServerErrors(func() { n++ })(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}
}

func TestCountServerErrorsNilCallback(t *testing.T) {
	h := CountServerErrors(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

func TestPanicCountsAsPanicAndServerError(t *testing.T) {
	var panics, errs int
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := CountServerErrors(func() { errs++ })(
		RecoveryLogger(logger, nil, OnPanic(func() { panics++ }))(
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if panics != 1 || errs != 1 || rec.Code != http.StatusInternalServerError {
		t.Fatalf("panics=%d errs=%d code=%d", panics, errs, rec.Code)
	}
}

func TestCountServerErrorsKeepsFlushing(t *testing.T) {
	h := CountServerErrors(func() {})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("x"))
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush through wrapper: %v", err)
		}
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !rec.Flushed {
		t.Fatal("not flushed")
	}
}
