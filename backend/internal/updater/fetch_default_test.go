//go:build !hatamoto_testkeys

package updater

import "testing"

// The default build never accepts plain http, not even to loopback.
func TestDefaultBuildRejectsHTTPUpdateURL(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:8080/releases", "http://localhost/releases"} {
		if _, err := NewFetcher(u); err == nil {
			t.Errorf("%s accepted in the default build", u)
		}
	}
	if _, err := NewFetcher(""); err != nil {
		t.Errorf("default URL: %v", err)
	}
	if _, _, ok := testInterval(); ok || clockOffset() != 0 || allowLoopbackHTTP() {
		t.Error("a test seam is active in the default build")
	}
}
