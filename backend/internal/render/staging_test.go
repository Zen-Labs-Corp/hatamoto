package render

import (
	"testing"

	"hatamoto.dev/core/internal/runenv"
)

func TestSEOHeadRobotsInStaging(t *testing.T) {
	want := seoHead(hreflangView(), false).Robots // production value
	if want == "noindex" {
		t.Fatalf("production robots = %q, test needs a different baseline", want)
	}
	t.Setenv(runenv.EnvVar, "staging")
	if got := seoHead(hreflangView(), false).Robots; got != "noindex" {
		t.Fatalf("staging robots = %q", got)
	}
	v := hreflangView()
	v.Preview = true
	if got := seoHead(v, false).Robots; got != "noindex,follow" {
		t.Fatalf("staging preview robots = %q", got)
	}
	t.Setenv(runenv.EnvVar, "")
	if got := seoHead(hreflangView(), false).Robots; got != want {
		t.Fatalf("production robots changed: %q vs %q", got, want)
	}
}
