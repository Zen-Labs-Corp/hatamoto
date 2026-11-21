package purge_test

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/purgestate"
	"hatamoto.dev/core/purge"
)

type fakePurger struct{ got []purge.Request }

func (f *fakePurger) Purge(_ context.Context, r purge.Request) error {
	f.got = append(f.got, r)
	return nil
}

func reset(t *testing.T) {
	t.Helper()
	purge.ResetForTest()
	t.Cleanup(purge.ResetForTest)
}

func TestRegisterVisibleToCore(t *testing.T) {
	reset(t)
	f := &fakePurger{}
	purge.Register("edge-cache", f)
	name, p, ok := purgestate.Registered()
	if !ok || name != "edge-cache" {
		t.Fatalf("Registered = %q %v", name, ok)
	}
	if err := p.Purge(context.Background(), purgestate.Request{URLs: []string{"https://example.com/a"}}); err != nil {
		t.Fatal(err)
	}
	if len(f.got) != 1 || f.got[0].URLs[0] != "https://example.com/a" {
		t.Errorf("got %+v", f.got)
	}
}

func TestRegisterAfterFreezePanics(t *testing.T) {
	reset(t)
	purge.FreezeForTest()
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	purge.Register("edge-cache", &fakePurger{})
}
