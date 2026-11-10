//go:build integration

package packages

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
)

// devRig adds a developer index (signed by a listing key) to a rig. The rig's
// main registry stays empty, so the developer entries are never shadowed.
type devRig struct {
	*rig
	listing, dev Key
	srv          *httptest.Server

	mu     sync.Mutex
	idx    Index
	served map[string][]byte
}

func newDevRig(t *testing.T) *devRig {
	t.Helper()
	r := newRig(t)
	now := time.Now().UTC()
	r.idx = Index{Version: 1, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339), Packages: []Entry{}}
	d := &devRig{rig: r, listing: newKey(t), dev: newKey(t), served: map[string][]byte{}}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		if req.URL.Path == "/developer.json" {
			idx := d.idx
			now := time.Now().UTC()
			idx.Version, idx.IssuedAt, idx.ExpiresAt = 1, now.Add(-time.Minute).Format(time.RFC3339), now.Add(24*time.Hour).Format(time.RFC3339)
			doc, err := SignIndex(mustJSON(idx), d.listing)
			if err != nil {
				t.Error(err)
			}
			_, _ = w.Write(doc)
			return
		}
		b, ok := d.served[req.URL.Path]
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(d.srv.Close)
	r.mgr.DeveloperRegistry = &Registry{URL: d.srv.URL + "/developer.json", Client: d.srv.Client(), Trust: trust(t, d.listing), AllowInsecure: true, Limits: DefaultLimits}
	return d
}

// pack builds the capa plugin at version, signed by key.
func (d *devRig) pack(version string, key Key, edit func(*sdk.Manifest)) []byte {
	d.t.Helper()
	files := d.plugin(version)
	if edit != nil {
		var m sdk.Manifest
		if err := jsonx.Unmarshal(files[PluginManifest], &m); err != nil {
			d.t.Fatal(err)
		}
		edit(&m)
		files[PluginManifest] = mustJSON(m)
	}
	raw, err := PackFiles(files, &key)
	if err != nil {
		d.t.Fatal(err)
	}
	return raw
}

// list serves raw and lists it in the developer index under listedKey, then
// refreshes the manager's cached index.
func (d *devRig) list(raw []byte, listedKey Key) {
	d.t.Helper()
	p, err := Parse(raw, DefaultLimits)
	if err != nil {
		d.t.Fatal(err)
	}
	sum := p.SHA256()
	path := "/" + p.Name + "-" + p.Version + Extension
	d.mu.Lock()
	d.served[path] = raw
	d.idx.Packages = []Entry{{Kind: KindPlugin, Name: p.Name, Version: p.Version, URL: path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(raw)), Author: "Acme", SignerKey: listedKey.Public}}
	d.mu.Unlock()
	if _, err := d.mgr.DeveloperIndex(context.Background(), true); err != nil {
		d.t.Fatal(err)
	}
}

var capaGrant = []string{sdk.PermKV, sdk.PermDB}

func (d *devRig) loaded(name string) bool {
	for _, s := range d.rt.Statuses() {
		if s.Name == name && s.Loaded {
			return true
		}
	}
	return false
}

func TestDeveloperInstallNeedsTheSwitchAndTheListedKey(t *testing.T) {
	d := newDevRig(t)
	ctx := context.Background()
	d.list(d.pack("1.0.0", d.dev, nil), d.dev)

	// Switch off (the default): refused with the client-view message.
	_, err := d.mgr.InstallFromRegistry(ctx, KindPlugin, "capa", InstallRequest{Grant: capaGrant})
	if !errors.Is(err, ErrDeveloperOff) || !strings.Contains(err.Error(), "Allow developer packages") {
		t.Fatalf("switch off: %v", err)
	}
	// An upload of the same signed package follows the same rules.
	if _, err := d.mgr.Install(ctx, d.pack("1.0.0", d.dev, nil), InstallRequest{Source: SourceUpload}); !errors.Is(err, ErrDeveloperOff) {
		t.Fatalf("upload with the switch off: %v", err)
	}
	// Unsigned and self-built paths are unchanged.
	if _, err := d.mgr.Install(ctx, mustPack(t, d.plugin("1.0.0")), InstallRequest{Source: SourceUpload}); !errors.Is(err, ErrUnsignedNotAllowed) {
		t.Fatalf("unsigned upload: %v", err)
	}

	if err := d.mgr.SetAllowDeveloper(ctx, true); err != nil {
		t.Fatal(err)
	}
	// Signed by another key than the index lists: refused even with the switch on.
	other := newKey(t)
	_, err = d.mgr.Install(ctx, d.pack("1.0.0", other, nil), InstallRequest{Source: SourceUpload})
	if !errors.Is(err, ErrDeveloperKeyMismatch) || !strings.Contains(err.Error(), other.ID) || !strings.Contains(err.Error(), d.dev.ID) {
		t.Fatalf("key mismatch: %v", err)
	}

	got, err := d.mgr.InstallFromRegistry(ctx, KindPlugin, "capa", InstallRequest{Grant: capaGrant})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == nil || !got.Status.Loaded {
		t.Fatalf("status: %+v", got.Status)
	}
	row, err := d.mgr.Store().Get(ctx, KindPlugin, "capa")
	if err != nil {
		t.Fatal(err)
	}
	if !row.Developer || row.DeveloperKey != d.dev.Public || row.SignerKeyID != d.dev.ID || ClassifyRow(row) != OriginDeveloper {
		t.Fatalf("row: %+v", row)
	}
	if !d.mgr.CommunityPlugin("capa") {
		t.Error("an installed developer plugin must get the community policy")
	}
}

func TestDeveloperPackageGetsTheCommunityCSPPolicy(t *testing.T) {
	d := newDevRig(t)
	ctx := context.Background()
	if err := d.mgr.SetAllowDeveloper(ctx, true); err != nil {
		t.Fatal(err)
	}
	raw := d.pack("1.0.0", d.dev, func(m *sdk.Manifest) { m.CSP = &sdk.CSP{ScriptSrc: []string{"https://js.example.com"}} })
	d.list(raw, d.dev)
	if _, err := d.mgr.Install(ctx, raw, InstallRequest{Source: SourceUpload}); !errors.Is(err, ErrCommunityCSP) {
		t.Fatalf("script host in csp: %v, want ErrCommunityCSP", err)
	}
}

func TestDeveloperSwitchOffKeepsInstalledPluginsAndPinsUpdates(t *testing.T) {
	d := newDevRig(t)
	ctx := context.Background()
	if err := d.mgr.SetAllowDeveloper(ctx, true); err != nil {
		t.Fatal(err)
	}
	d.list(d.pack("1.0.0", d.dev, nil), d.dev)
	if _, err := d.mgr.InstallFromRegistry(ctx, KindPlugin, "capa", InstallRequest{Grant: capaGrant}); err != nil {
		t.Fatal(err)
	}

	// Switch off: the plugin keeps running through a reconcile.
	if err := d.mgr.SetAllowDeveloper(ctx, false); err != nil {
		t.Fatal(err)
	}
	d.mgr.Reconcile(ctx)
	if !d.loaded("capa") || d.mgr.Blocked(KindPlugin, "capa") != "" {
		t.Fatalf("switch off stopped the plugin: loaded=%v blocked=%q", d.loaded("capa"), d.mgr.Blocked(KindPlugin, "capa"))
	}

	// An update is refused while off, allowed when on again.
	d.list(d.pack("1.3.0", d.dev, nil), d.dev)
	if _, err := d.mgr.InstallFromRegistry(ctx, KindPlugin, "capa", InstallRequest{}); !errors.Is(err, ErrDeveloperOff) {
		t.Fatalf("update with the switch off: %v", err)
	}
	if err := d.mgr.SetAllowDeveloper(ctx, true); err != nil {
		t.Fatal(err)
	}
	got, err := d.mgr.InstallFromRegistry(ctx, KindPlugin, "capa", InstallRequest{})
	if err != nil || !got.Updated || got.Version != "1.3.0" {
		t.Fatalf("update with the switch on: %+v, %v", got, err)
	}

	// The index lists a new key for 1.4.0 and it is signed by it: the key
	// differs from the installed version's, so the update is refused and
	// Confirm does not bypass it.
	next := newKey(t)
	raw := d.pack("1.4.0", next, nil)
	d.list(raw, next)
	for _, confirm := range []bool{false, true} {
		_, err := d.mgr.Install(ctx, raw, InstallRequest{Source: SourceUpload, Confirm: confirm})
		if !errors.Is(err, ErrDeveloperSignerChanged) {
			t.Fatalf("signer change (confirm=%v): %v", confirm, err)
		}
	}
	row, _ := d.mgr.Store().Get(ctx, KindPlugin, "capa")
	if row.Version != "1.3.0" || row.SignerKeyID != d.dev.ID {
		t.Fatalf("a refused update changed the row: %+v", row)
	}
}

// After a reboot the index may be unreachable: the row alone decides.
func TestDeveloperRowReloadsAtBootWithoutTheIndex(t *testing.T) {
	d := newDevRig(t)
	ctx := context.Background()
	if err := d.mgr.SetAllowDeveloper(ctx, true); err != nil {
		t.Fatal(err)
	}
	d.list(d.pack("1.0.0", d.dev, nil), d.dev)
	if _, err := d.mgr.InstallFromRegistry(ctx, KindPlugin, "capa", InstallRequest{Grant: capaGrant}); err != nil {
		t.Fatal(err)
	}
	if err := d.mgr.SetAllowDeveloper(ctx, false); err != nil {
		t.Fatal(err)
	}
	d.rt.Unload(ctx, "capa")
	d.srv.Close() // the index (and the main registry's dependency on it) is gone

	o := d.mgr.Options
	o.Registry = &Registry{URL: "http://127.0.0.1:1/registry.json", AllowInsecure: true}
	o.DeveloperRegistry = &Registry{URL: "http://127.0.0.1:1/developer.json", Trust: trust(t, d.listing), AllowInsecure: true}
	m2 := NewManager(o)
	m2.Reconcile(ctx)

	if !d.loaded("capa") || m2.Blocked(KindPlugin, "capa") != "" {
		t.Fatalf("the stored developer plugin did not load at boot: loaded=%v blocked=%q", d.loaded("capa"), m2.Blocked(KindPlugin, "capa"))
	}
	if !m2.CommunityPlugin("capa") || !m2.ScriptsRefused("capa") {
		t.Error("after a reboot the plugin must still get the community policy")
	}
}

// The install prompt classifies a developer package as one, so it applies the
// same community policy Install does (and so offers no confirmation for an
// update the key pin refuses).
func TestPolicyOriginOfADeveloperPackage(t *testing.T) {
	d := newDevRig(t)
	ctx := context.Background()
	if err := d.mgr.SetAllowDeveloper(ctx, true); err != nil {
		t.Fatal(err)
	}
	raw := d.pack("1.0.0", d.dev, func(m *sdk.Manifest) {
		m.Permissions = append(m.Permissions, "builder.island")
		m.CSP = &sdk.CSP{ScriptSrc: []string{"https://js.example.com"}}
	})
	d.list(raw, d.dev)
	p, err := Parse(raw, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	origin, err := d.mgr.PolicyOrigin(ctx, p, SourceUpload)
	if err != nil || origin != OriginDeveloper {
		t.Fatalf("origin %v, err %v", origin, err)
	}
	if got := RefusedPermissions(origin, p); len(got) != 1 || got[0] != "builder.island" {
		t.Fatalf("refused = %v", got)
	}
	if err := CheckCommunityCSP(origin, p); err == nil {
		t.Fatal("script host passed the prompt")
	}
}
