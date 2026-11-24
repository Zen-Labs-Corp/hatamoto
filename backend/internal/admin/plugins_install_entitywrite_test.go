package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/permtext"
	"hatamoto.dev/sdk"
)

func TestPluginInstallEntityWritePrompt(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	rec := postMultipart(g, "/admin/plugins/upload", "owner-token", "p.hmp", "plugin-entity-write:hello")
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	for _, want := range []string{"Creates and edits content in its own content type", "Handle with care", permtext.Lookup(sdk.PermEntitiesWriteOwn).Help, "Content type: Stories (story)", "Edit or delete pages you wrote"} {
		if !strings.Contains(body, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if strings.Contains(body, "Change your live site directly") {
		t.Errorf("the prompt still lists the live-site line:\n%s", body)
	}
	a11yOK(t, body)
}

func TestPluginPromptNamesEntityWriteUpdate(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	rec := postMultipart(g, "/admin/plugins/upload", "owner-token", "p.hmp", "plugin-write-update:hello")
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	for _, want := range []string{"needs your confirmation", "<code>entities.write.own</code>", "<code>story</code>", `name="confirm"`} {
		if !strings.Contains(body, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	a11yOK(t, body)
}

func TestPluginPromptNamesEntitySchemaChange(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	rec := postMultipart(g, "/admin/plugins/upload", "owner-token", "p.hmp", "plugin-write-widen:hello")
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	for _, want := range []string{"<code>note</code>", "instead of <code>story</code>", "<code>text</code>"} {
		if !strings.Contains(body, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	a11yOK(t, body)
}
