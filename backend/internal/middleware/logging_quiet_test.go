package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveLogged(t *testing.T, quiet bool) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := RequestLogger(logger, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/about", nil)
	if quiet {
		req = req.WithContext(WithoutRequestLog(req.Context()))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, buf.String()
}

func TestRequestLogger_WithoutRequestLogSkips(t *testing.T) {
	rec, logs := serveLogged(t, true)
	if logs != "" {
		t.Errorf("quiet request wrote log output: %q", logs)
	}
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("response = %d %q, want 200 ok", rec.Code, rec.Body.String())
	}
}

func TestRequestLogger_DefaultStillLogs(t *testing.T) {
	_, logs := serveLogged(t, false)
	if n := strings.Count(logs, "http request"); n != 1 {
		t.Errorf("log lines = %d, want 1: %q", n, logs)
	}
}
