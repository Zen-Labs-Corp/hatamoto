package worker

import (
	"os"
	"testing"
	"time"
)

func TestClaimLease(t *testing.T) {
	for _, c := range []struct{ every, jitter, want time.Duration }{
		{time.Minute, 0, 54 * time.Second},
		{6 * time.Hour, time.Hour, 4*time.Hour + 30*time.Minute},
		{time.Second, 900 * time.Millisecond, time.Second},
	} {
		if got := claimLease(c.every, c.jitter); got != c.want {
			t.Errorf("claimLease(%v, %v) = %v, want %v", c.every, c.jitter, got, c.want)
		}
	}
}

func TestDefaultHolder(t *testing.T) {
	t.Setenv("HATAMOTO_INSTANCE_ID", " a-1 ")
	if got := DefaultHolder(); got != "a-1" {
		t.Fatalf("DefaultHolder = %q, want a-1", got)
	}
	t.Setenv("HATAMOTO_INSTANCE_ID", "")
	want, err := os.Hostname()
	if err != nil || want == "" {
		want = "local"
	}
	if got := DefaultHolder(); got != want {
		t.Fatalf("DefaultHolder = %q, want %q", got, want)
	}
}
