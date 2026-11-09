package wasmhost

import (
	"context"
	"strings"
	"sync"
	"testing"

	"hatamoto.dev/sdk"
)

// recordErrors installs an observer for the test and returns the plugin names
// it saw.
func recordErrors(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	SetErrorObserver(func(plugin string) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, plugin)
	})
	t.Cleanup(func() { SetErrorObserver(nil) })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

func TestErrorObserverTrapAndGuestError(t *testing.T) {
	seen := recordErrors(t)
	h := newHost(t)
	load(t, h, "bad", sdk.Limits{MemoryPages: 256, HookTimeoutsMS: timeouts(300, "hostile_panic", "guest_error")})

	if _, err := call(h, "bad", "hostile_panic", ""); err == nil {
		t.Fatal("trap did not fail")
	}
	if got := seen(); len(got) != 1 || got[0] != "bad" {
		t.Fatalf("after a trap observer saw %v, want [bad]", got)
	}
	if _, err := call(h, "bad", "guest_error", ""); err == nil {
		t.Fatal("guest error did not fail")
	}
	if got := seen(); len(got) != 2 {
		t.Fatalf("after a guest error observer saw %v, want 2 calls", got)
	}
}

func TestErrorObserverIgnoresRejectAndSuccess(t *testing.T) {
	seen := recordErrors(t)
	h := newHost(t)
	m := hookManifest("rej", sdk.Limits{}, map[string]string{"mode": "reject"})
	if err := h.Load(context.Background(), m, hostileWasm); err != nil {
		t.Fatal(err)
	}
	load(t, h, "bad", sdk.Limits{})

	if _, err := call(h, "rej", "filter_entity_before_save", "{}"); err == nil || !strings.HasPrefix(err.Error(), sdk.RejectPrefix) {
		t.Fatalf("want a rejection, got %v", err)
	}
	if _, err := call(h, "bad", "echo", "{}"); err != nil {
		t.Fatal(err)
	}
	if got := seen(); len(got) != 0 {
		t.Fatalf("observer saw %v for a rejection and a success", got)
	}
}

func TestErrorObserverNilAndPanicking(t *testing.T) {
	SetErrorObserver(nil)
	h := newHost(t)
	load(t, h, "bad", sdk.Limits{})
	if _, err := call(h, "bad", "guest_error", ""); err == nil {
		t.Fatal("guest error did not fail")
	}
	SetErrorObserver(func(string) { panic("observer bug") })
	t.Cleanup(func() { SetErrorObserver(nil) })
	if _, err := call(h, "bad", "guest_error", ""); err == nil || strings.Contains(err.Error(), "observer bug") {
		t.Fatalf("observer panic leaked: %v", err)
	}
}
