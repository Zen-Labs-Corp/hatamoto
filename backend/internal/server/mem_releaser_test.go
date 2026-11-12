package server

import (
	"testing"
	"time"
)

func TestActivityMiddlewareStampsRequest(t *testing.T) {
	h, s := bootRoutes(t)
	if s.stopProbes != nil {
		t.Cleanup(s.stopProbes)
	}
	before := time.Now().UnixNano()
	hit(h, "GET", "/healthz")
	if got := s.lastReq.Load(); got < before {
		t.Fatalf("lastReq = %d, want >= %d", got, before)
	}
}
