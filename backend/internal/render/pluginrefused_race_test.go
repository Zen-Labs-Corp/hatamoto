package render

import (
	"sync"
	"testing"
	"time"
)

// A compile that finishes after RemovePlugin must not recreate the removed
// plugin's record. The hook orders the remove inside the compile's window.
func TestCompileRacingRemovePluginLeavesNoRecord(t *testing.T) {
	t.Parallel()
	reg := newPluginRegistry()
	if err := reg.registerSection("acme-gone", "gone", `{{define "section-gone"}}{{if}}{{end}}`); err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(Options{}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.templatesRefused("acme-gone"); len(got) != 1 {
		t.Fatalf("setup: acme-gone refused = %q", got)
	}
	// A new registration bumps gen, so current() rebuilds.
	if err := reg.registerSection("acme-stay", "stay", `{{define "section-stay"}}{{if}}{{end}}`); err != nil {
		t.Fatal(err)
	}
	reached, removed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	reg.beforeNote = func() {
		once.Do(func() {
			close(reached)
			<-removed
		})
	}
	go func() {
		<-reached
		reg.removePlugin("acme-gone")
		close(removed)
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.current()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second): // hang guard, not ordering
		t.Fatal("compile blocked")
	}
	if got := reg.templatesRefused("acme-gone"); got != nil {
		t.Errorf("removed plugin has a record: %q", got)
	}
	reg.mu.RLock()
	_, ok := reg.compiled["acme-gone"]
	reg.mu.RUnlock()
	if ok {
		t.Error("compiled record recreated for a removed plugin")
	}
	if got := reg.templatesRefused("acme-stay"); len(got) != 1 {
		t.Errorf("live plugin lost its record: %q", got)
	}
}

func TestCompileNoteSkipsPluginsWithoutOwnership(t *testing.T) {
	t.Parallel()
	reg := newPluginRegistry()
	reg.noteCompileRefused([]string{"ghost"}, map[string][]string{"ghost": {"x"}})
	if got := reg.templatesRefused("ghost"); got != nil {
		t.Errorf("record for a plugin that owns nothing: %q", got)
	}
}
