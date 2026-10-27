package app

import (
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/adminpath"
)

// The demo refusals match /admin only, so demo mode ignores the pin and the
// stored address and writes nothing.
func TestDemoBootForcesDefaultAddress(t *testing.T) {
	st := &fakeAdminStore{cur: adminpath.Stored{Path: strp("/k7qm-x2rf-9tdc")}}
	lg, buf := bufLogger()
	got, err := bootAdminPath(context.Background(), true, st, staticSite{}, owner(false), "/pinned-place-x7q2", lg)
	if err != nil || got != "/admin" {
		t.Fatalf("got %q, %v", got, err)
	}
	if st.pendingCalls != 0 || len(st.sets) != 0 {
		t.Error("demo boot must not write the database")
	}
	if !strings.Contains(buf.String(), "demo mode: the admin is served at /admin") {
		t.Errorf("missing log line: %s", buf)
	}
}

func TestNonDemoBootResolvesNormally(t *testing.T) {
	st := &fakeAdminStore{cur: adminpath.Stored{Path: strp("/k7qm-x2rf-9tdc")}}
	lg, _ := bufLogger()
	got, err := bootAdminPath(context.Background(), false, st, staticSite{}, owner(true), "", lg)
	if err != nil || got != "/k7qm-x2rf-9tdc" {
		t.Fatalf("got %q, %v", got, err)
	}
}
