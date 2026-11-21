package render

import (
	"strings"
	"testing"
)

func TestRefreshRecordsWithoutARender(t *testing.T) {
	t.Parallel()
	reg := newPluginRegistry()
	e, err := newEngine(Options{}, reg)
	if err != nil {
		t.Fatal(err)
	}
	// Registers fine, but vet rejects it at compile time.
	if err := reg.registerSection("acme-late", "late", `<p>no entry</p>`); err != nil {
		t.Fatal(err)
	}
	if got := reg.templatesRefused("acme-late"); got != nil {
		t.Fatalf("before Refresh: %q, want nothing compiled yet", got)
	}
	e.Refresh()
	if got := reg.templatesRefused("acme-late"); len(got) != 1 || !strings.Contains(got[0], `must define "section-late"`) {
		t.Errorf("after Refresh: %q", got)
	}
}

func TestRefreshIsANoOpWhenUnchanged(t *testing.T) {
	t.Parallel()
	reg := newPluginRegistry()
	e, err := newEngine(Options{}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.registerSection("acme-late", "late", `{{define "section-late"}}<p>x</p>{{end}}`); err != nil {
		t.Fatal(err)
	}
	e.Refresh()
	e.mu.RLock()
	first := e.cur
	e.mu.RUnlock()
	e.Refresh()
	e.mu.RLock()
	second := e.cur
	e.mu.RUnlock()
	if first != second {
		t.Error("Refresh rebuilt an unchanged registry")
	}
}
