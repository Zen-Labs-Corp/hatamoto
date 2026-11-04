//go:build integration

package packages

import (
	"context"
	"encoding/json"
	"testing"

	"hatamoto.dev/sdk"
)

// approve records an active approval the way the owner's approval would.
func (r *rig) approve(plugin string) {
	r.t.Helper()
	if _, err := r.pool.Exec(context.Background(),
		`INSERT INTO hatamoto_standing_approvals (plugin, run, policy, fingerprint) VALUES ($1, 'sync', '{}', '{}')`, plugin); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) activeApprovals(plugin string) int {
	r.t.Helper()
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM hatamoto_standing_approvals WHERE plugin = $1 AND voided_at IS NULL`, plugin).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func TestStandingApprovalVoid(t *testing.T) {
	r := newRig(t)
	perms := []string{sdk.PermKV, sdk.PermDB, sdk.PermLog}
	install := func(version, title string) {
		t.Helper()
		files := r.plugin(version)
		files["ui/index.html"] = []byte("<h1>" + title + "</h1>")
		if err := r.installPlugin(files, perms, InstallRequest{Grant: perms, Confirm: true}); err != nil {
			t.Fatal(err)
		}
	}
	// The same bytes again are not a change.
	install("1.0.0", "capa")
	r.approve("capa")
	install("1.0.0", "capa")
	if got := r.activeApprovals("capa"); got != 1 {
		t.Fatalf("identical reinstall left %d active approvals, want 1", got)
	}

	t.Run("manifest", func(t *testing.T) {
		files := r.plugin("1.0.0")
		var man sdk.Manifest
		if err := json.Unmarshal(files[PluginManifest], &man); err != nil {
			t.Fatal(err)
		}
		man.UI = &sdk.AdminUI{Title: "Capa renamed"}
		files[PluginManifest] = mustJSON(man)
		if err := r.installPlugin(files, perms, InstallRequest{Grant: perms, Confirm: true}); err != nil {
			t.Fatal(err)
		}
		if got := r.activeApprovals("capa"); got != 0 {
			t.Fatalf("a manifest change with the same version left %d active approvals", got)
		}
	})

	t.Run("version", func(t *testing.T) {
		r.approve("capa")
		install("2.0.0", "capa")
		if got := r.activeApprovals("capa"); got != 0 {
			t.Fatalf("a version change left %d active approvals", got)
		}
	})

	t.Run("uninstall", func(t *testing.T) {
		r.approve("capa")
		if err := r.mgr.Uninstall(context.Background(), KindPlugin, "capa", false); err != nil {
			t.Fatal(err)
		}
		if got := r.activeApprovals("capa"); got != 0 {
			t.Fatalf("uninstall left %d active approvals", got)
		}
	})
}
