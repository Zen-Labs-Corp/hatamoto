package indexnow

import (
	"context"
	"io"
	"testing"

	"hatamoto.dev/core/internal/runenv"
)

func TestStagingSuppressesIndexNow(t *testing.T) {
	t.Setenv(runenv.EnvVar, "staging")
	res := fakeRes{cfg: cfg(), on: true, urls: func(Item) []string { return []string{"https://example.com/a"} }}
	r, c := setup(t, res, io.Discard)
	r.Notify(Item{Kind: "published"})
	r.Flush(context.Background())
	if c.count() != 0 {
		t.Fatalf("%s: requests = %d, want 0", runenv.IndexNow, c.count())
	}
}
