package packages

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/tags"
)

func tagDeclWith(hosts, category string) string {
	return `{"name":"Acme","id":{"label":"Site ID","pattern":"^[0-9]{4,9}$"},"script":"https://cdn.acme.example/{id}.js","hosts":[` + hosts + `],"category":"` + category + `"}`
}

func tagPackParsed(t *testing.T, name, version, decl string) *Package {
	t.Helper()
	raw, err := PackFiles(map[string][]byte{
		PluginManifest:   []byte(`{"name":"` + name + `","version":"` + version + `","sdk":"^0.11"}`),
		"tags/acme.json": []byte(decl),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return parse(t, raw)
}

func tagIDs() []string {
	var ids []string
	for _, v := range tags.Packs() {
		ids = append(ids, v.ID)
	}
	return ids
}

func TestReconcileTagPacksFollowsRowState(t *testing.T) {
	t.Cleanup(func() { tags.SetPacks(nil) })
	p := tagPackParsed(t, "acme", "1.0.0", tagDeclWith(`"cdn.acme.example"`, "analytics"))
	m := &Manager{Logger: slog.Default(), loaded: map[string]loadedPlugin{"acme": {sha: "h1", pkg: p}}, blocked: map[string]string{}}
	row := func(mut func(*Row)) []Row {
		r := Row{Kind: KindPlugin, Name: "acme", Version: "1.0.0", SHA256: "h1", Enabled: true, State: StateInstalled}
		if mut != nil {
			mut(&r)
		}
		return []Row{r}
	}
	ctx := context.Background()
	step := func(rows []Row, wantChange bool, wantIDs ...string) {
		t.Helper()
		if got := m.reconcileTagPacks(ctx, rows); got != wantChange {
			t.Fatalf("changed = %v, want %v", got, wantChange)
		}
		if got := tagIDs(); !slices.Equal(got, wantIDs) {
			t.Fatalf("pack vendors = %v, want %v", got, wantIDs)
		}
	}
	step(row(nil), true, "acme.acme")
	if v := tags.Packs()[0]; v.PluginVersion != "1.0.0" || v.Boot != "generic" {
		t.Fatalf("pack vendor = %+v", v)
	}
	if h := (tags.Settings{Vendors: map[string]tags.VendorState{"acme.acme": {Enabled: true, ID: "1234"}}}).Hosts(); len(h.Script) != 1 {
		t.Fatalf("hosts = %+v", h)
	}
	step(row(nil), false, "acme.acme")                  // nothing changed
	step(row(func(r *Row) { r.Enabled = false }), true) // disabled: gone
	step(row(nil), true, "acme.acme")                   // enabled again
	m.blocked["plugin/acme"] = "policy"
	step(row(nil), true) // blocked: gone
	delete(m.blocked, "plugin/acme")
	step(row(nil), true, "acme.acme")
	step(row(func(r *Row) { r.State = StateUninstalling }), true) // uninstalling: gone
	step(nil, false)                                              // row deleted: still none
}

func TestReadTagPackSkipsRefusedAndNoTagPackages(t *testing.T) {
	var logs bytes.Buffer
	m := &Manager{Logger: slog.New(slog.NewTextHandler(&logs, nil)), loaded: map[string]loadedPlugin{"old-sync": {sha: "h1"}}}
	if e := m.readTagPack(context.Background(), Row{Kind: KindPlugin, Name: "old-sync", SHA256: "h1"}); e.sha != "h1" || len(e.vendors) != 0 || logs.Len() != 0 {
		t.Fatalf("entry = %+v, logs %q", e, logs.String())
	}
	raw, err := PackFiles(map[string][]byte{PluginManifest: []byte(`{"name":"hello","version":"1.0.0","sdk":"^0.11"}`), PluginWasm: []byte("\x00asm")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.loaded["hello"] = loadedPlugin{sha: "h2", pkg: parse(t, raw)}
	if e := m.readTagPack(context.Background(), Row{Kind: KindPlugin, Name: "hello", SHA256: "h2"}); e.sha != "h2" || len(e.vendors) != 0 {
		t.Fatalf("entry = %+v", e)
	}
}

func TestNewTagAccessNeedsConfirmation(t *testing.T) {
	old := tagPackParsed(t, "acme", "1.0.0", tagDeclWith(`"cdn.acme.example"`, "analytics"))
	same := tagPackParsed(t, "acme", "1.1.0", tagDeclWith(`"cdn.acme.example"`, "analytics"))
	if hosts, cat := newTagAccess(old, same); len(hosts) != 0 || cat {
		t.Fatalf("unchanged pack: %v %v", hosts, cat)
	}
	fewer := tagPackParsed(t, "acme", "1.1.0", tagDeclWith(`"cdn.acme.example"`, "analytics"))
	if hosts, _ := newTagAccess(tagPackParsed(t, "acme", "1.0.0", tagDeclWith(`"cdn.acme.example","other.acme.example"`, "analytics")), fewer); len(hosts) != 0 {
		t.Fatalf("dropping a host asked for confirmation: %v", hosts)
	}
	wider := tagPackParsed(t, "acme", "1.1.0", tagDeclWith(`"cdn.acme.example","*.acme.example"`, "analytics"))
	if hosts, cat := newTagAccess(old, wider); !slices.Equal(hosts, []string{"https://*.acme.example"}) || cat {
		t.Fatalf("new host: %v %v", hosts, cat)
	}
	moved := tagPackParsed(t, "acme", "1.1.0", tagDeclWith(`"cdn.acme.example"`, "marketing"))
	if hosts, cat := newTagAccess(old, moved); len(hosts) != 0 || !cat {
		t.Fatalf("category change: %v %v", hosts, cat)
	}
}

func TestTagPackAuditDetailAndCount(t *testing.T) {
	t.Cleanup(func() { tags.SetPacks(nil) })
	p := tagPackParsed(t, "acme", "1.0.0", tagDeclWith(`"cdn.acme.example","a.acme.example"`, "analytics"))
	d := map[string]any{}
	addTagDetail(d, p)
	if d["tags"] != 1 || !slices.Equal(d["tag_hosts"].([]string), []string{"a.acme.example", "cdn.acme.example"}) {
		t.Fatalf("detail = %v", d)
	}
	none := map[string]any{}
	addTagDetail(none, parse(t, mustPack(t, map[string][]byte{PluginManifest: []byte(`{"name":"hello","version":"1.0.0","sdk":"^0.11"}`), PluginWasm: []byte("\x00asm")})))
	if len(none) != 0 {
		t.Fatalf("detail for a wasm plugin = %v", none)
	}
	vs, _ := p.TagVendors()
	tags.SetPacks([]tags.PackVendor{{Vendor: vs[0]}})
	if tagPackCount("acme") != 1 || tagPackCount("acm") != 0 || tagPackCount("other") != 0 {
		t.Fatal("tagPackCount")
	}
	if !strings.HasPrefix(vs[0].ID, "acme.") {
		t.Fatal(vs[0].ID)
	}
}

func mustPack(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	raw, err := PackFiles(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
