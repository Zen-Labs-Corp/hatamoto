package admin

import (
	"context"
	"encoding/csv"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

func TestEntityExportChooserGolden(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	body := c.get(t, "owner-token", "/admin/entities/export?type=recipe", "entities-export")
	has(t, body, `<form method="get" action="/admin/entities/export.csv"`, `name="type" value="recipe"`,
		`<legend class="hm-field__label">Columns to include</legend>`, `value="attr:minutes"`, `value="attr:difficulty"`,
		`name="status"`, "Download CSV")
	if rec := c.do("GET", "/admin/entities/export?type=nope", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown type = %d", rec.Code)
	}
	if rec := c.do("GET", "/admin/entities/export?type=block", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("reuse type = %d", rec.Code)
	}
}

func TestEntityExportStreamsChosenColumns(t *testing.T) {
	t.Parallel()
	var events []AuditEvent
	c := newContentRigWith(t, func(d *Deps) {
		d.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }
	})
	c.ents.addEntity(21, c.recipe, "=cmd|calc", "cmd", entity.StatusPublished, `{"minutes":-5,"difficulty":"easy"}`, t0)
	c.ents.addEntity(22, c.recipe, "Pie", "pie", entity.StatusDraft, `{"minutes":30}`, t0)

	q := url.Values{"type": {"recipe"}, "col": {"name", "status", "attr:minutes"}}
	rec := c.do("GET", "/admin/entities/export.csv?"+q.Encode(), "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("export = %d\n%s", rec.Code, rec.Body)
	}
	h := rec.Header()
	if !strings.HasPrefix(h.Get("Content-Type"), "text/csv") || !strings.Contains(h.Get("Content-Disposition"), `attachment; filename="recipe-`) || h.Get("Cache-Control") != "no-store" {
		t.Errorf("headers = %v", h)
	}
	raw := rec.Body.String()
	if !strings.HasPrefix(raw, "\xEF\xBB\xBF") {
		t.Error("no BOM")
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(raw, "\xEF\xBB\xBF"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rows[0], ","); got != "name,status,attr:minutes" {
		t.Errorf("header = %s", got)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %v", rows)
	}
	seen := map[string]string{}
	for _, r := range rows[1:] {
		seen[r[0]] = r[2]
	}
	if seen["'=cmd|calc"] != "'-5" || seen["Pie"] != "30" {
		t.Errorf("cells = %v (formulas must be neutralised)", seen)
	}
	if len(events) != 1 || events[0].Action != "entity.export_csv" || events[0].Target != "recipe" || events[0].Detail["rows"] != 2 || events[0].Detail["columns"] != 3 {
		t.Errorf("audit = %+v", events)
	}

	q.Set("status", "draft")
	rec = c.do("GET", "/admin/entities/export.csv?"+q.Encode(), "owner-token", nil, nil)
	if strings.Contains(rec.Body.String(), "cmd") || !strings.Contains(rec.Body.String(), "Pie") {
		t.Errorf("status filter: %s", rec.Body)
	}
}

func TestEntityExportRejectsBadInput(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	for name, path := range map[string]string{
		"unknown column": "/admin/entities/export.csv?type=recipe&col=name&col=attr:nope",
		"no column":      "/admin/entities/export.csv?type=recipe",
		"bad status":     "/admin/entities/export.csv?type=recipe&col=name&status=weird",
	} {
		if rec := c.do("GET", path, "owner-token", nil, nil); rec.Code != 400 {
			t.Errorf("%s = %d", name, rec.Code)
		}
	}
	if rec := c.do("GET", "/admin/entities/export.csv?type=block&col=name", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("reuse type = %d", rec.Code)
	}
}

func TestEntityExportRoles(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	path := "/admin/entities/export.csv?type=recipe&col=name"
	if rec := c.do("GET", path, "editor-token", nil, nil); rec.Code != 200 {
		t.Errorf("editor = %d", rec.Code)
	}
	// Owner and editor are the only roles; anyone else is not signed in.
	for _, p := range []string{path, "/admin/entities/export?type=recipe"} {
		if rec := c.do("GET", p, "", nil, nil); rec.Code != 303 {
			t.Errorf("signed out %s = %d", p, rec.Code)
		}
	}
	list := c.get(t, "owner-token", "/admin/entities?type=recipe", "")
	has(t, list, `href="/admin/entities/export?type=recipe"`, "Export CSV")
	if strings.Contains(c.get(t, "owner-token", "/admin/entities", ""), "Export CSV") {
		t.Error("the combined list offers an export")
	}
}
