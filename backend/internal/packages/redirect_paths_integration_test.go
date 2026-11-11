//go:build integration

package packages

import (
	"context"
	"errors"
	"testing"
)

func TestRedirectPathsConflictAcrossInstall(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	install := func(name, version string, paths ...string) error {
		r.t.Helper()
		return r.installPack(pathsPack(name, version, paths...))
	}
	if err := install("alpha", "1.0.0", "/go"); err != nil {
		t.Fatal(err)
	}
	if err := install("beta", "1.0.0", "/go"); !errors.Is(err, ErrRedirectPathTaken) {
		t.Fatalf("second install of /go: %v, want ErrRedirectPathTaken", err)
	}
	if err := install("beta", "1.0.0", "/start"); err != nil {
		t.Fatal(err)
	}
	// An upgrade onto a path another plugin holds is refused.
	if err := install("beta", "1.1.0", "/start", "/go"); !errors.Is(err, ErrRedirectPathTaken) {
		t.Fatalf("upgrade onto /go: %v, want ErrRedirectPathTaken", err)
	}
	// A disabled owner still holds its paths.
	if err := r.mgr.SetEnabled(ctx, "alpha", false); err != nil {
		t.Fatal(err)
	}
	if err := install("gamma", "1.0.0", "/go"); !errors.Is(err, ErrRedirectPathTaken) {
		t.Fatalf("install over a disabled owner: %v, want ErrRedirectPathTaken", err)
	}
	// A core-owned path is refused at install.
	if err := install("delta", "1.0.0", "/admin"); err == nil {
		t.Fatal("a core-owned path was accepted")
	}
	// Enabling re-checks: beta now holds /start; plant a row for alpha that
	// declares it too, as a stale store could, and enabling is refused.
	raw, err := PackFiles(pathsPack("alpha", "1.0.0", "/go", "/start"), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := parse(t, raw)
	if err := r.mgr.store.put(ctx, r.pool, p, SourceDev, false, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.SetEnabled(ctx, "alpha", true); !errors.Is(err, ErrRedirectPathTaken) {
		t.Fatalf("enable with an overlapping plugin: %v, want ErrRedirectPathTaken", err)
	}
}
