package server

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestWarnStoreFullIsRateLimited(t *testing.T) {
	var buf bytes.Buffer
	s := &Server{logger: slog.New(slog.NewTextHandler(&buf, nil))}
	for range 5 {
		s.warnStoreFull()
	}
	if n := strings.Count(buf.String(), "upload store is full"); n != 1 {
		t.Fatalf("logged %d warnings, want 1", n)
	}
}
