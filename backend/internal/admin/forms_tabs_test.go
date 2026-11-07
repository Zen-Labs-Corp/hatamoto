package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/form"
)

func TestFormTabsPages(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	base := "/admin/forms/design/" + fm.ID.String()
	for _, tab := range []string{"settings", "share"} {
		rec := g.do("GET", base+"/"+tab, "owner-token", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", tab, rec.Code)
		}
		if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %v", tab, issues)
		}
		if n := strings.Count(rec.Body.String(), `class="hm-tabs__tab"`); n != 4 {
			t.Errorf("%s: tab strip has %d items, want 4", tab, n)
		}
	}
	for _, p := range []string{base + "/nope", "/admin/forms/design/" + uuid.NewString() + "/settings", "/admin/forms/design/" + uuid.NewString() + "/share"} {
		if rec := g.do("GET", p, "owner-token", nil, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, rec.Code)
		}
	}
}

func TestFormSettingsBoot(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	ff.addSub(fm, form.SubmissionStatusSpam, time.Now(), map[string]any{"name": "Spammer"})
	ff.addSub(fm, form.SubmissionStatusNew, time.Now().Add(-time.Hour), map[string]any{"name": "Ada"})
	settings := g.do("GET", "/admin/forms/design/"+fm.ID.String()+"/settings", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`data-hm-fd-boot`, `"mode":"settings"`, `"sample":{"name":"Ada"}`, `"webhooks":[]`, `"spamUrl":"/admin/forms/spam"`} {
		if !strings.Contains(settings, want) {
			t.Errorf("settings boot lacks %s", want)
		}
	}
	if strings.Contains(g.do("GET", "/admin/forms/design/"+fm.ID.String()+"/settings", "editor-token", nil, nil).Body.String(), "spamUrl") {
		t.Error("an editor is offered the spam settings link")
	}
	if build := g.do("GET", "/admin/forms/design/"+fm.ID.String(), "owner-token", nil, nil).Body.String(); strings.Contains(build, `"mode"`) || strings.Contains(build, `"sample"`) {
		t.Error("the Build boot carries Settings data")
	}
}

// The Settings page carries the Build editor bar and the controls the script binds.
func TestFormSettingsPageHasEditorBar(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	body := g.do("GET", "/admin/forms/design/"+fm.ID.String()+"/settings", "owner-token", nil, nil).Body.String()
	for _, hook := range []string{"data-hm-fd-title", "data-hm-fd-status", "data-hm-fd-state", "data-hm-fd-undo", "data-hm-fd-redo", "data-hm-fd-save"} {
		if !strings.Contains(body, hook) {
			t.Errorf("settings lacks the editor bar hook %s", hook)
		}
	}
	for _, want := range []string{`data-hm-ti`, `data-hm-fs="notify_email"`, `data-hm-fs-seg="retention_days"`, `data-hm-fs-token="{{name}}"`, `action="/admin/forms/design/` + fm.ID.String() + `/duplicate"`, `Email (the visitor)`} {
		if !strings.Contains(body, want) {
			t.Errorf("settings lacks %s", want)
		}
	}
	if strings.Contains(body, "View on site") || strings.Contains(body, `name="version"`) {
		t.Error("settings shows View on site or the old version field")
	}
}

func TestFormSettingsOldPostIsGone(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	rec := g.do("POST", "/admin/forms/design/"+fm.ID.String()+"/settings", "owner-token", url.Values{"name": {"A"}}, nil)
	if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
		t.Errorf("POST settings: %d, want 405/404", rec.Code)
	}
}
