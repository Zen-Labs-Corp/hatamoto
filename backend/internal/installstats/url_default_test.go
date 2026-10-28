//go:build !hatamoto_testkeys

package installstats

import "testing"

func TestDefaultBuildRejectsLoopbackHTTP(t *testing.T) {
	t.Parallel()
	got, warn := ResolveURL(envOf(map[string]string{EnvURL: "http://127.0.0.1:8080/ping"}))
	if got != DefaultURL {
		t.Errorf("url = %q, want the default", got)
	}
	if warn == "" {
		t.Error("want a warning")
	}
}
