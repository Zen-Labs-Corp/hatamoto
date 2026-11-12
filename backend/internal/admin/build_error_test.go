package admin

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// An embedded build error cannot heal, so it is logged once, not on every
// request. Not parallel: it swaps the process-wide embedded cache.
func TestEmbeddedBuildErrorIsLoggedOnce(t *testing.T) {
	saved := embedded.srcs
	savedErr := embedded.err
	t.Cleanup(func() {
		embedded.once = sync.Once{}
		embedded.once.Do(func() {})
		embedded.srcs, embedded.err = saved, savedErr
	})
	embedded.once = sync.Once{}
	embedded.once.Do(func() { embedded.srcs, embedded.err = nil, errors.New("broken page source") })

	var buf bytes.Buffer
	g := newRig(t, func(d *Deps) { d.Logger = slog.New(slog.NewTextHandler(&buf, nil)) })
	for range 3 {
		if as, pages := g.a.state(); as == nil || pages != nil {
			t.Fatalf("state = %v, %v; want empty assets and no pages", as, pages)
		}
	}
	if n := strings.Count(buf.String(), "admin build failed"); n != 1 {
		t.Fatalf("logged %d times, want 1:\n%s", n, buf.String())
	}
}
