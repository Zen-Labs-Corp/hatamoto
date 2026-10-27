package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testRedact(p string) string {
	if p == "/hq-x7k" || strings.HasPrefix(p, "/hq-x7k/") {
		return "<admin>" + strings.TrimPrefix(p, "/hq-x7k")
	}
	return p
}

func TestRequestLoggerRedactsPath(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := RequestLogger(logger, testRedact)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/hq-x7k/settings?x=1", nil))
	out := buf.String()
	if !strings.Contains(out, "path=<admin>/settings") || strings.Contains(out, "hq-x7k") {
		t.Fatalf("path not redacted:\n%s", out)
	}
}

func TestRequestLoggerNilRedactKeepsPath(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := RequestLogger(logger, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/hq-x7k/settings", nil))
	if !strings.Contains(buf.String(), "path=/hq-x7k/settings") {
		t.Fatalf("path changed:\n%s", buf.String())
	}
}

func TestRecoveryLoggerRedactsPath(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := RecoveryLogger(logger, testRedact)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/hq-x7k/settings", nil))
	out := buf.String()
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(out, "<admin>/settings") || strings.Contains(out, "hq-x7k") {
		t.Fatalf("panic log not redacted:\n%s", out)
	}
}

// The redacted path is computed before the handler runs, so a prefix swapped
// during the request still logs the one that was live on entry.
func TestLoggersRedactBeforeHandler(t *testing.T) {
	for _, name := range []string{"request", "recovery"} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, nil))
			prefix := "/old-aaa"
			redact := func(p string) string { return strings.Replace(p, prefix, "<admin>", 1) }
			inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				prefix = "/new-bbb"
				if name == "recovery" {
					panic("boom")
				}
			})
			var h http.Handler
			if name == "request" {
				h = RequestLogger(logger, redact)(inner)
			} else {
				h = RecoveryLogger(logger, redact)(inner)
			}
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/old-aaa/x", nil))
			if out := buf.String(); !strings.Contains(out, "path=<admin>/x") || strings.Contains(out, "old-aaa") {
				t.Fatalf("redaction not computed before the handler:\n%s", out)
			}
		})
	}
}
