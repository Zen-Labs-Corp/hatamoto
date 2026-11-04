//go:build integration

package packages

import (
	"context"
	"slices"
	"testing"
)

func TestUninstallClearsTagSettings(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	var asked []string
	r.mgr.DropTagSettings = func(_ context.Context, plugin string) ([]string, error) {
		asked = append(asked, plugin)
		return []string{plugin + ".acme"}, nil
	}
	p := tagPackParsed(t, "acme-tags", "1.0.0", tagDeclWith(`"cdn.acme.example"`, "analytics"))
	if err := r.mgr.store.Put(ctx, p, SourceUpload, true); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.Uninstall(ctx, KindPlugin, "acme-tags", UninstallOpts{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(asked, []string{"acme-tags"}) {
		t.Fatalf("DropTagSettings asked for %v", asked)
	}
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'tracking.pack_clear'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit rows = %d, %v", n, err)
	}
}
