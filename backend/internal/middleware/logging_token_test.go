package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Reset and invite tokens travel in the query string. The request logger logs
// the path only, so a token never reaches the logs; this pins that.
func TestRequestLoggerNeverLogsQueryTokens(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := RequestLogger(logger, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("GET", "/admin/reset?token=SECRET-TOKEN-VALUE&next=/admin", nil)
	req.Header.Set("Referer", "https://example.test/admin/accept?token=SECRET-TOKEN-VALUE")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if strings.Contains(buf.String(), "SECRET-TOKEN-VALUE") || strings.Contains(buf.String(), "token=") {
		t.Fatalf("token leaked into the request log:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "/admin/reset") {
		t.Fatalf("path not logged:\n%s", buf.String())
	}
}
