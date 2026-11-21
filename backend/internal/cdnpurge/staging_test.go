package cdnpurge

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/runenv"
)

func TestStagingSuppressesPurge(t *testing.T) {
	t.Setenv(runenv.EnvVar, "staging")
	tg := &fakeTarget{name: "a"}
	r := newRunner(tg)
	r.Notify(Item{Paths: []string{"https://example.com/a"}})
	r.Notify(Item{All: true})
	r.Flush(context.Background())
	if n := len(tg.calls()); n != 0 {
		t.Fatalf("%s: sends = %d, want 0", runenv.Purge, n)
	}
}
