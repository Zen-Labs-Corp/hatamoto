//go:build integration

package packages

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"hatamoto.dev/core/internal/pluginhost"
)

// lockedBuffer is a log sink the manager and the test may use concurrently.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// revoke serves a developer index that revokes rv and reads it.
func (d *devRig) revoke(rv ...Revocation) {
	d.t.Helper()
	d.mu.Lock()
	d.idx.Revoked = rv
	d.mu.Unlock()
	if _, err := d.mgr.DeveloperIndex(context.Background(), true); err != nil {
		d.t.Fatal(err)
	}
}

// installedDev installs the developer plugin capa 1.0.0 with the switch on.
func installedDev(t *testing.T) (*devRig, *lockedBuffer) {
	t.Helper()
	d := newDevRig(t)
	ctx := context.Background()
	logs := &lockedBuffer{}
	d.mgr.Logger = slog.New(slog.NewTextHandler(logs, nil))
	if err := d.mgr.SetAllowDeveloper(ctx, true); err != nil {
		t.Fatal(err)
	}
	d.list(d.pack("1.0.0", d.dev, nil), d.dev)
	if _, err := d.mgr.InstallFromRegistry(ctx, KindPlugin, "capa", InstallRequest{Grant: capaGrant}); err != nil {
		t.Fatal(err)
	}
	return d, logs
}

func TestRevocationByKeyDisablesTheRowAndKeepsItsData(t *testing.T) {
	d, logs := installedDev(t)
	ctx := context.Background()

	d.revoke(Revocation{SignerKey: d.dev.Public, Reason: "key leaked"})

	row, err := d.mgr.Store().Get(ctx, KindPlugin, "capa")
	if err != nil {
		t.Fatal(err)
	}
	if row.Enabled || row.DisabledReason != "key leaked" {
		t.Fatalf("row after revocation: enabled=%v reason=%q", row.Enabled, row.DisabledReason)
	}
	if d.loaded("capa") {
		t.Error("the revoked plugin is still loaded")
	}
	var kept bool
	if err := d.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = $1 AND tablename = 'notes')`, pluginhost.SchemaName("capa")).Scan(&kept); err != nil || !kept {
		t.Errorf("the plugin's data was not kept: %v, %v", kept, err)
	}
	for _, want := range []string{"level=WARN", `msg="developer package disabled: revoked by the registry"`, "plugin=capa", "key=" + d.dev.ID, `reason="key leaked"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, logs.String())
		}
	}
	var by string
	if err := d.pool.QueryRow(ctx, `SELECT actor FROM hatamoto_admin_audit WHERE action = 'package.revoke_disable' AND target = 'plugin:capa'`).Scan(&by); err != nil || by != "registry" {
		t.Errorf("audit: actor=%q err=%v", by, err)
	}

	// Still revoked: cannot be switched back on.
	if err := d.mgr.SetEnabled(ctx, "capa", true); !errors.Is(err, ErrRevoked) || !strings.Contains(err.Error(), "key leaked") {
		t.Fatalf("re-enable while revoked: %v", err)
	}
	// The index becomes unreachable: still refused.
	d.srv.Config.Close()
	if err := d.mgr.SetEnabled(ctx, "capa", true); !errors.Is(err, ErrRevoked) {
		t.Fatalf("re-enable with the index unreachable: %v", err)
	}
}

func TestRevocationByNameAndVersionOnlyHitsThatVersionAndLifts(t *testing.T) {
	d, _ := installedDev(t)
	ctx := context.Background()

	d.revoke(Revocation{Name: "capa", Version: "0.9.0", Reason: "old bug"})
	if row, _ := d.mgr.Store().Get(ctx, KindPlugin, "capa"); !row.Enabled || row.DisabledReason != "" {
		t.Fatalf("another version's revocation touched the row: %+v", row)
	}
	// A name without a version revokes nothing.
	d.revoke(Revocation{Name: "capa", Reason: "no version"})
	if row, _ := d.mgr.Store().Get(ctx, KindPlugin, "capa"); !row.Enabled {
		t.Fatalf("a revocation without a version disabled the row: %+v", row)
	}

	d.revoke(Revocation{Name: "capa", Version: "1.0.0", Reason: "bad build"})
	if row, _ := d.mgr.Store().Get(ctx, KindPlugin, "capa"); row.Enabled || row.DisabledReason != "bad build" {
		t.Fatalf("row: %+v", row)
	}

	// The registry withdraws the revocation: the owner can switch it back on.
	d.revoke()
	if err := d.mgr.SetEnabled(ctx, "capa", true); err != nil {
		t.Fatal(err)
	}
	row, _ := d.mgr.Store().Get(ctx, KindPlugin, "capa")
	if !row.Enabled || row.DisabledReason != "" || !d.loaded("capa") {
		t.Fatalf("after lifting: %+v loaded=%v", row, d.loaded("capa"))
	}
}

func TestRevocationNeverTouchesFirstPartyPackages(t *testing.T) {
	d := newDevRig(t)
	ctx := context.Background()
	if err := d.installPlugin(d.plugin("1.0.0"), capaGrant, InstallRequest{Source: SourceUpload}); err != nil {
		t.Fatal(err)
	}
	d.revoke(Revocation{SignerKey: d.key.Public, Reason: "x"}, Revocation{Name: "capa", Version: "1.0.0", Reason: "x"})
	if row, _ := d.mgr.Store().Get(ctx, KindPlugin, "capa"); !row.Enabled || row.DisabledReason != "" {
		t.Fatalf("a first-party row was revoked: %+v", row)
	}
}

func TestRevokedActiveThemeIsDeactivated(t *testing.T) {
	d := newDevRig(t)
	ctx := context.Background()
	if err := d.mgr.SetAllowDeveloper(ctx, true); err != nil {
		t.Fatal(err)
	}
	files := d.theme("1.0.0")
	raw, err := PackFiles(files, &d.dev)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(raw, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	sum := p.SHA256()
	d.mu.Lock()
	d.served["/sunrise.hmp"] = raw
	d.idx.Packages = []Entry{{Kind: KindTheme, Name: "sunrise", Version: "1.0.0", URL: "/sunrise.hmp", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(raw)), Author: "Acme", SignerKey: d.dev.Public}}
	d.mu.Unlock()
	if _, err := d.mgr.DeveloperIndex(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.mgr.Install(ctx, raw, InstallRequest{Source: SourceUpload}); err != nil {
		t.Fatal(err)
	}
	if err := d.mgr.Activate(ctx, "sunrise", true); err != nil {
		t.Fatal(err)
	}

	d.revoke(Revocation{SignerKey: d.dev.Public, Reason: "key leaked"})

	cfg, err := d.mgr.Store().Config(ctx)
	if err != nil || cfg.ActiveTheme != "" {
		t.Fatalf("active theme after revocation: %q, %v", cfg.ActiveTheme, err)
	}
	row, _ := d.mgr.Store().Get(ctx, KindTheme, "sunrise")
	if row.DisabledReason != "key leaked" {
		t.Fatalf("row: %+v", row)
	}
	if err := d.mgr.Usable(row); err == nil {
		t.Error("a revoked theme is still usable")
	}
	if err := d.mgr.Activate(ctx, "sunrise", true); !errors.Is(err, ErrRevoked) {
		t.Fatalf("activate while revoked: %v", err)
	}
	d.revoke()
	if err := d.mgr.Activate(ctx, "sunrise", true); err != nil {
		t.Fatalf("activate after the revocation lifted: %v", err)
	}
}
