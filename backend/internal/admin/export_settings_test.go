package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

func TestExportInfoPage(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	rec := g.do("GET", "/admin/settings/export", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "hatamoto export site.tar.gz") || !strings.Contains(body, "hatamoto import site.tar.gz") || !strings.Contains(body, `data-width="narrow"`) {
		t.Fatalf("owner: %d", rec.Code)
	}
	if n := strings.Count(body, "data-hm-copy-field"); n != 4 {
		t.Errorf("copy buttons = %d, want one per command", n)
	}
	if !strings.Contains(body, "hm-callout--warning") || !strings.Contains(body, "visitor data") {
		t.Error("the visitor-data note is not a warning callout")
	}
	if strings.Contains(body, `action="/admin/settings/export`) {
		t.Error("the export tab is informational: no form posts")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if rec := g.do("GET", "/admin/settings/export", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor: %d, want 403", rec.Code)
	}
	if rec := g.do("POST", "/admin/settings/export", "owner-token", nil, nil); rec.Code == 200 || rec.Code == 303 {
		t.Errorf("POST must not exist: %d", rec.Code)
	}
}
