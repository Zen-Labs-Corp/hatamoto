//go:build integration

package packages

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/sdk"
)

func TestAuditRecordsActorName(t *testing.T) {
	r := newRig(t)
	ctx := WithActor(context.Background(), Actor{ID: uuid.New(), Email: "owner@example.test", Name: "Pat Owner"})
	until := time.Now().Add(time.Hour).UTC()
	if err := r.mgr.SetAllowUnsigned(ctx, true, &until, "1h"); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.SetAllowUnsigned(ctx, false, nil, ""); err != nil {
		t.Fatal(err)
	}
	rows, err := r.pool.Query(ctx, `SELECT detail->>'by', detail->>'allow', COALESCE(detail->>'until', ''), COALESCE(detail->>'duration', '')
		FROM hatamoto_admin_audit WHERE action = 'package.allow_unsigned' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got [][4]string
	for rows.Next() {
		var row [4]string
		if err := rows.Scan(&row[0], &row[1], &row[2], &row[3]); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	want := [][4]string{
		{"Pat Owner", "true", until.Format(time.RFC3339), "1h"},
		{"Pat Owner", "false", "", ""},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("audit rows = %v, want %v", got, want)
	}

	// The CLI actor has no name and records no "by".
	if err := r.mgr.SetAllowUnsigned(WithActor(context.Background(), Actor{Email: "cli"}), true, nil, "forever"); err != nil {
		t.Fatal(err)
	}
	var by *string
	var until2 string
	if err := r.pool.QueryRow(ctx, `SELECT detail->>'by', detail->>'until' FROM hatamoto_admin_audit WHERE actor = 'cli' AND action = 'package.allow_unsigned'`).Scan(&by, &until2); err != nil {
		t.Fatal(err)
	}
	if by != nil || until2 != "never" {
		t.Errorf("cli row: by=%v until=%q", by, until2)
	}
}

func TestExpiredUnsignedAllowanceRefusesInstall(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	raw := mustPack(t, r.theme("1.0.0"))
	future := time.Now().Add(time.Hour).UTC()
	if err := r.mgr.SetAllowUnsigned(ctx, true, &future, "1h"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.mgr.Install(ctx, raw, InstallRequest{Source: SourceUpload}); err != nil {
		t.Fatalf("allowance still running: %v", err)
	}
	_ = r.mgr.Uninstall(ctx, KindTheme, "sunrise", false)
	if _, err := r.pool.Exec(ctx, `UPDATE hatamoto_package_config SET allow_unsigned_until = now() - interval '1 minute' WHERE singleton`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.mgr.Install(ctx, raw, InstallRequest{Source: SourceUpload}); !errors.Is(err, ErrUnsignedNotAllowed) {
		t.Fatalf("expired allowance: err = %v, want ErrUnsignedNotAllowed", err)
	}
}

func TestAdminPathNamesAreReservedForPlugins(t *testing.T) {
	r := newRig(t)
	for _, name := range []string{"browse", "security"} {
		files := r.plugin("1.0.0")
		files[PluginManifest] = mustJSON(sdk.Manifest{Name: name, Version: "1.0.0", SDK: "^0.7", Permissions: []string{sdk.PermLog}})
		raw, err := PackFiles(files, &r.key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.mgr.Install(context.Background(), raw, InstallRequest{Source: SourceUpload}); !errors.Is(err, ErrReservedName) {
			t.Errorf("%s: err = %v, want ErrReservedName", name, err)
		}
	}
}

// A plugin installed under a name that was reserved later is not stranded:
// it still updates and uninstalls.
func TestInstalledReservedNameStillUpdatesAndUninstalls(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	pack := func(version string) []byte {
		files := r.plugin(version)
		files[PluginManifest] = mustJSON(sdk.Manifest{Name: "browse", Version: version, SDK: "^0.7", Permissions: []string{sdk.PermLog}})
		raw, err := PackFiles(files, &r.key)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	old, err := Parse(pack("1.0.0"), DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.store.Put(ctx, old, SourceUpload, true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.mgr.Install(ctx, pack("1.1.0"), InstallRequest{Source: SourceUpload}); err != nil {
		t.Fatalf("update of an installed reserved name: %v", err)
	}
	if err := r.mgr.Uninstall(ctx, KindPlugin, "browse", false); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := r.mgr.Install(ctx, pack("1.0.0"), InstallRequest{Source: SourceUpload}); !errors.Is(err, ErrReservedName) {
		t.Fatalf("new install after removal: err = %v, want ErrReservedName", err)
	}
}
