//go:build integration

package packages

import (
	"context"
	"testing"

	"hatamoto.dev/sdk"
)

// TestUninstallDetachesPluginCommenters: after an uninstall the plugin's
// commenters have no origin and their sessions are revoked, so a later package
// with the same name cannot inherit either. Another plugin's commenters stay.
func TestUninstallDetachesPluginCommenters(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	perms := []string{sdk.PermKV, sdk.PermDB, sdk.PermLog}
	if err := r.installPlugin(r.plugin("1.0.0"), perms, InstallRequest{Grant: perms, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := r.pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO hatamoto_commenters (email_hash, email_sealed, origin_plugin, origin_account)
		VALUES ('plugin:00000000-0000-0000-0000-000000000001', ''::bytea, 'capa', 'a1'),
		       ('plugin:00000000-0000-0000-0000-000000000002', ''::bytea, 'other', 'a1')`)
	exec(`INSERT INTO hatamoto_commenters (email_hash, email_sealed, origin_plugin, origin_account) VALUES ('linked', '\x00', 'capa', 'a2')`)

	if err := r.mgr.Uninstall(ctx, KindPlugin, "capa", UninstallOpts{}); err != nil {
		t.Fatal(err)
	}
	type row struct {
		plugin  *string
		account *string
		version int
	}
	get := func(hash string) row {
		t.Helper()
		var x row
		if err := r.pool.QueryRow(ctx, `SELECT origin_plugin, origin_account, token_version FROM hatamoto_commenters WHERE email_hash = $1`, hash).
			Scan(&x.plugin, &x.account, &x.version); err != nil {
			t.Fatal(err)
		}
		return x
	}
	for _, h := range []string{"plugin:00000000-0000-0000-0000-000000000001", "linked"} {
		if x := get(h); x.plugin != nil || x.account != nil || x.version != 2 {
			t.Errorf("%s after uninstall: origin %v/%v token_version %d, want NULL/NULL 2", h, x.plugin, x.account, x.version)
		}
	}
	if x := get("plugin:00000000-0000-0000-0000-000000000002"); x.plugin == nil || x.version != 1 {
		t.Errorf("another plugin's commenter was touched: %v token_version %d", x.plugin, x.version)
	}
}
