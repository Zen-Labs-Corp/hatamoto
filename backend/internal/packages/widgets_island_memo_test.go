package packages

import (
	"context"
	"encoding/json/jsontext"
	"log/slog"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/render"
)

const (
	islandManifest   = `{"name":"a","sdk":"^0.10","permissions":["builder.island"]}`
	noIslandManifest = `{"name":"b","sdk":"^0.10","permissions":[]}`
)

func islandRow(name, sha, manifest string) Row {
	return Row{Kind: KindPlugin, Name: name, SHA256: sha, Enabled: true, State: StateInstalled,
		Manifest: jsontext.Value(manifest)}
}

func TestIslandPluginsDecodesOncePerPackage(t *testing.T) {
	m := &Manager{Logger: slog.Default(), Grants: fakeGrants{}}
	rows := []Row{islandRow("a", "h1", islandManifest), islandRow("b", "h2", noIslandManifest)}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range 10 {
		if !m.islandPlugins(rows) {
			t.Fatalf("call %d: island not found", i)
		}
	}
	if m.widgets.islandDecodes != 2 {
		t.Fatalf("decodes = %d, want 2", m.widgets.islandDecodes)
	}
}

func TestIslandPluginsRedecodesOnChange(t *testing.T) {
	m := &Manager{Logger: slog.Default(), Grants: fakeGrants{}}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.islandPlugins([]Row{islandRow("a", "h1", islandManifest)}) || m.widgets.islandDecodes != 1 {
		t.Fatalf("first call: decodes = %d", m.widgets.islandDecodes)
	}
	if m.islandPlugins([]Row{islandRow("a", "h1b", noIslandManifest)}) || m.widgets.islandDecodes != 2 {
		t.Fatalf("new sha: decodes = %d, want 2 and no island", m.widgets.islandDecodes)
	}
	if !m.islandPlugins([]Row{islandRow("a", "h1b", islandManifest)}) || m.widgets.islandDecodes != 3 {
		t.Fatalf("same sha, new manifest: decodes = %d, want 3 and island", m.widgets.islandDecodes)
	}
}

func TestReconcileWidgetsPrunesIslandMemo(t *testing.T) {
	t.Cleanup(func() { builder.SetPackKinds(nil); render.SetPackWidgets(nil) })
	m := &Manager{Logger: slog.Default(), Grants: fakeGrants{}}
	m.widgets.cache = map[string]widgetEntry{"a": {sha: "h1"}, "b": {sha: "h2"}}
	rows := []Row{islandRow("a", "h1", islandManifest), islandRow("b", "h2", noIslandManifest)}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconcileWidgets(context.Background(), rows)
	if len(m.widgets.islands) != 2 {
		t.Fatalf("memo size = %d, want 2", len(m.widgets.islands))
	}
	m.reconcileWidgets(context.Background(), rows[:1])
	if _, ok := m.widgets.islands["b"]; len(m.widgets.islands) != 1 || ok {
		t.Fatalf("memo not pruned: %v", m.widgets.islands)
	}
}
