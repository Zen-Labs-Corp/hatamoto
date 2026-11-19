package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLogRecordsCapRequestFields(t *testing.T) {
	long := strings.Repeat("é", 32*1024) // 64 KiB of multi-byte runes
	const limit = 4096

	t.Run("request log", func(t *testing.T) {
		var buf bytes.Buffer
		h := RequestLogger(slog.New(slog.NewTextHandler(&buf, nil)), nil)(
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		req := httptest.NewRequest(http.MethodGet, "/"+long, nil)
		req.Header.Set("User-Agent", long)
		h.ServeHTTP(httptest.NewRecorder(), req)
		if buf.Len() > limit {
			t.Fatalf("record is %d bytes, want <= %d", buf.Len(), limit)
		}
		if !utf8.Valid(buf.Bytes()) {
			t.Fatal("record is not valid UTF-8")
		}
	})

	t.Run("recovery log", func(t *testing.T) {
		var buf bytes.Buffer
		h := RecoveryLogger(slog.New(slog.NewJSONHandler(&buf, nil)), nil)(
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
		req := httptest.NewRequest(http.MethodGet, "/"+long, nil)
		h.ServeHTTP(httptest.NewRecorder(), req)
		if buf.Len() > limit {
			t.Fatalf("record is %d bytes, want <= %d", buf.Len(), limit)
		}
	})
}
