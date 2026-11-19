package wasmhost

import (
	"context"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// startLoopModule is a module whose start section never returns:
// (func loop br 0 end) (start 0).
var startLoopModule = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type () -> ()
	0x03, 0x02, 0x01, 0x00, // one function of type 0
	0x08, 0x01, 0x00, // start = function 0
	0x0a, 0x09, 0x01, 0x07, 0x00, 0x03, 0x40, 0x0c, 0x00, 0x0b, 0x0b, // loop; br 0; end; end
}

func TestStartSectionIsBounded(t *testing.T) {
	h := newHost(t)
	type result struct {
		err     error
		elapsed time.Duration
	}
	done := make(chan result, 1)
	go func() {
		begin := time.Now()
		err := h.Load(context.Background(), manifest("startloop", sdk.Limits{}), startLoopModule)
		done <- result{err, time.Since(begin)}
	}()
	limit := maxStartWait + 3*time.Second
	select {
	case r := <-done:
		if r.err == nil {
			t.Fatal("Load of a module with a non-terminating start section succeeded")
		}
		if !strings.Contains(r.err.Error(), "deadline exceeded") {
			t.Fatalf("error does not report the deadline: %v", r.err)
		}
		if r.elapsed > limit {
			t.Fatalf("Load took %s, want at most %s", r.elapsed, limit)
		}
	case <-time.After(limit):
		t.Fatal("Load did not return")
	}
	if got := h.Loaded(); len(got) != 0 {
		t.Fatalf("Loaded() = %v, want empty", got)
	}
}
