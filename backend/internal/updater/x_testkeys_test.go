//go:build hatamoto_testkeys

package updater

import (
	"testing"
	"time"
)

func TestSeamsReadTheirEnvironment(t *testing.T) {
	if !allowLoopbackHTTP() {
		t.Error("loopback http must be allowed in test builds")
	}
	if _, err := NewFetcher("http://127.0.0.1:9/releases"); err != nil {
		t.Errorf("loopback http: %v", err)
	}
	if _, err := NewFetcher("http://example.test/releases"); err == nil {
		t.Error("non-loopback http accepted")
	}

	t.Setenv("HATAMOTO_TEST_UPDATE_INTERVAL", "")
	if _, _, ok := testInterval(); ok {
		t.Error("unset interval reported")
	}
	t.Setenv("HATAMOTO_TEST_UPDATE_INTERVAL", "5s")
	if every, first, ok := testInterval(); !ok || every != 5*time.Second || first != 3*time.Second {
		t.Errorf("5s: %v %v %v", every, first, ok)
	}
	t.Setenv("HATAMOTO_TEST_UPDATE_INTERVAL", "1s")
	if every, first, ok := testInterval(); !ok || every != time.Second || first != time.Second {
		t.Errorf("1s: %v %v %v", every, first, ok)
	}
	for _, bad := range []string{"soon", "0s", "-5s"} {
		t.Setenv("HATAMOTO_TEST_UPDATE_INTERVAL", bad)
		if _, _, ok := testInterval(); ok {
			t.Errorf("%q accepted", bad)
		}
	}

	for in, want := range map[string]time.Duration{"": 0, "48h": 48 * time.Hour, "-48h": 0, "x": 0} {
		t.Setenv("HATAMOTO_TEST_CLOCK_OFFSET", in)
		if got := clockOffset(); got != want {
			t.Errorf("offset %q = %v, want %v", in, got, want)
		}
	}
}
