//go:build integration

package packages

import (
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

func anyContains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// An install lists the plugin's refused templates at once, with no page render
// in between, and neither the install nor an enable is blocked by them.
func TestInstallListsRefusedTemplatesAtOnce(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	t.Cleanup(func() { render.SetPluginScriptGate(nil); render.RemovePlugin("capa") })
	perms := []string{sdk.PermKV}

	files := r.plugin("1.0.0")
	files["sections/nodef.html"] = []byte("<p>no entry</p>")
	files["sections/big.html"] = []byte(`{{define "section-big"}}{{range 30000}}<span class="cell">0123456789</span>{{end}}{{end}}`)
	if err := r.installPlugin(files, perms, InstallRequest{}); err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		got := r.mgr.TemplatesRefused("capa")
		if !anyContains(got, `must define "section-nodef"`) {
			t.Fatalf("%s: no entry for the section without a definition: %v", when, got)
		}
		if !anyContains(got, "256 KiB output limit") {
			t.Fatalf("%s: no entry for the oversized section: %v", when, got)
		}
		if s := r.mgr.MarkupStopped("capa"); len(s) != 0 {
			t.Fatalf("%s: markup stopped: %v", when, s)
		}
	}
	check("install")

	if err := r.mgr.SetEnabled(ctx, "capa", false); err != nil {
		t.Fatal(err)
	}
	if got := r.mgr.TemplatesRefused("capa"); len(got) != 0 {
		t.Fatalf("disabled plugin still lists refusals: %v", got)
	}
	if err := r.mgr.SetEnabled(ctx, "capa", true); err != nil {
		t.Fatal(err)
	}
	if got := r.mgr.TemplatesRefused("capa"); !anyContains(got, `must define "section-nodef"`) {
		t.Fatalf("enable: refusal not back: %v", got)
	}

	if err := r.installPlugin(r.plugin("1.1.0"), perms, InstallRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := r.mgr.TemplatesRefused("capa"); got != nil {
		t.Fatalf("upgrade without the bad sections still lists: %v", got)
	}
}
