//go:build integration

package packages

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"hatamoto.dev/core/internal/tags"
)

// A tag pack from a developer origin is refused at install; one signed by a
// key the owner trusts (self-built) still installs.
func TestInstallRefusesTagPackFromRestrictedOrigin(t *testing.T) {
	d := newDevRig(t)
	ctx := context.Background()
	t.Cleanup(func() { tags.SetPacks(nil) })
	if err := d.mgr.SetAllowDeveloper(ctx, true); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("testdata", "tagpacks", "hotjar")

	raw, err := PackDir(dir, &d.dev)
	if err != nil {
		t.Fatal(err)
	}
	d.list(raw, d.dev)
	if _, err := d.mgr.Install(ctx, raw, InstallRequest{Source: SourceUpload}); !errors.Is(err, ErrCommunityTags) {
		t.Fatalf("developer tag pack: %v, want ErrCommunityTags", err)
	}

	own, err := PackDir(dir, &d.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.mgr.Install(ctx, own, InstallRequest{Source: SourceUpload}); err != nil {
		t.Fatalf("self-built tag pack: %v", err)
	}
}
