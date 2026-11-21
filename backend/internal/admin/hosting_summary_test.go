package admin

import (
	"strings"
	"testing"
	"time"
)

func TestHostingLast(t *testing.T) {
	t.Parallel()

	at := time.Now().Add(-2 * time.Minute)
	ok := HostingRun{Started: at, Trigger: "manual", Pages: 214, Bytes: 38 << 20, Duration: 9 * time.Second, Result: HostingRunOK}
	bad := HostingRun{Started: at, Trigger: "change", Errors: 1, FirstError: "upload refused.", Result: HostingRunFailed}
	testFail := bad
	testFail.Trigger = HostingTriggerTest
	testOK := ok
	testOK.Trigger = HostingTriggerTest
	running := HostingRun{Started: at, Trigger: "manual", Result: HostingRunRunning}

	t.Run("newest ok", func(t *testing.T) {
		t.Parallel()
		got := hostingLast([]HostingRun{ok, bad})
		if !strings.HasPrefix(got, "214 pages") || strings.Contains(got, "Failed") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("failed over ok", func(t *testing.T) {
		t.Parallel()
		got := hostingLast([]HostingRun{bad, ok})
		for _, want := range []string{"Failed 2 minutes ago", "upload refused", "last good: 214 pages"} {
			if !strings.Contains(got, want) {
				t.Errorf("%q lacks %q", got, want)
			}
		}
		if !strings.HasPrefix(got, "Failed ") || strings.Contains(got, "0 bytes") || strings.Contains(got, "refused.") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("failed only", func(t *testing.T) {
		t.Parallel()
		got := hostingLast([]HostingRun{bad})
		if !strings.HasPrefix(got, "Failed ") || strings.Contains(got, "last good") || strings.Contains(got, "bytes") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("test runs skipped", func(t *testing.T) {
		t.Parallel()
		got := hostingLast([]HostingRun{testFail, running, ok})
		if !strings.HasPrefix(got, "214 pages") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("nothing to show", func(t *testing.T) {
		t.Parallel()
		if got := hostingLast([]HostingRun{running, testOK}); got != "" {
			t.Errorf("got %q", got)
		}
		if got := hostingLast(nil); got != "" {
			t.Errorf("got %q", got)
		}
	})
}
