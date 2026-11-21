package admin

import (
	"net/url"
	"slices"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/sectiontemplate"
)

// designAuditRow checks that the n-th captured event (1-based) is the last one
// and has the wanted action, target and detail keys.
func designAuditRow(t *testing.T, read func() []AuditEvent, n int, action, target string, detail map[string]any) {
	t.Helper()
	evs := read()
	assertAuditSafe(t, evs)
	if len(evs) != n {
		t.Fatalf("events = %+v, want %d", evs, n)
	}
	ev := evs[n-1]
	if ev.Action != action || ev.Target != target || ev.Actor != "owner@example.com" {
		t.Fatalf("row = %+v, want %s on %s by the owner", ev, action, target)
	}
	for k, want := range detail {
		if ws, ok := want.([]string); ok {
			if gs, _ := ev.Detail[k].([]string); !slices.Equal(ws, gs) {
				t.Errorf("%s: detail[%s] = %v, want %v", target, k, ev.Detail[k], want)
			}
		} else if ev.Detail[k] != want {
			t.Errorf("%s: detail[%s] = %v, want %v", target, k, ev.Detail[k], want)
		}
	}
}

func TestDesignAuditTokens(t *testing.T) {
	t.Parallel()

	g, _, _ := tokensRig(t, true)
	read := auditCapture(g.a)
	if rec := g.do("POST", "/admin/theme/design-system", "owner-token", url.Values{"color_accent": {"not a colour;"}}, nil); rec.Code != 422 {
		t.Fatalf("invalid: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("failed save audited: %+v", evs)
	}
	if rec := g.do("POST", "/admin/theme/design-system", "owner-token", url.Values{"space_4": {"1.25rem"}, "space_2": {"0.5rem"}}, nil); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	// A value equal to the built-in default is not stored, so it is not a change.
	designAuditRow(t, read, 1, AuditSettingsUpdated, "design-system", map[string]any{"changed": []string{"space_4"}})
	if rec := g.do("POST", "/admin/theme/design-system/reset", "owner-token", url.Values{}, nil); rec.Code != 303 {
		t.Fatalf("reset: %d", rec.Code)
	}
	designAuditRow(t, read, 2, AuditSettingsUpdated, "design-system", map[string]any{"op": "reset"})
}

func TestDesignAuditParts(t *testing.T) {
	t.Parallel()

	r := newPartsRig(t, "header", "footer")
	read := auditCapture(r.a)
	if rec := r.do("POST", "/admin/theme/parts", "owner-token", url.Values{"part_header": {"nope"}}, nil); rec.Code != 422 {
		t.Fatalf("invalid: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("failed save audited: %+v", evs)
	}
	if rec := r.do("POST", "/admin/theme/parts", "owner-token", url.Values{"part_header": {r.header.ID.String()}}, nil); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	designAuditRow(t, read, 1, AuditSettingsUpdated, "layout-parts", map[string]any{"changed": []string{"header"}})
}

func TestDesignAuditPresets(t *testing.T) {
	t.Parallel()

	g, _, _ := presetsRig(t)
	read := auditCapture(g.a)
	bad := url.Values{"new_name": {"Bad Name"}, "new_label": {"x"}, "new_key": {"pt"}, "new_value": {"space-1"}}
	if rec := g.do("POST", "/admin/theme/presets/new", "owner-token", bad, nil); rec.Code != 422 {
		t.Fatalf("invalid add: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("failed add audited: %+v", evs)
	}
	if rec := g.do("POST", "/admin/theme/presets", "owner-token", url.Values{"label.card": {"Card v2"}}, nil); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	designAuditRow(t, read, 1, AuditSettingsUpdated, "presets", map[string]any{"changed": []string{"card"}})
	add := url.Values{"new_name": {"hero-band"}, "new_label": {"Hero band"}, "new_key": {"bg"}, "new_value": {"accent"}}
	if rec := g.do("POST", "/admin/theme/presets/new", "owner-token", add, nil); rec.Code != 303 {
		t.Fatalf("add: %d", rec.Code)
	}
	designAuditRow(t, read, 2, AuditSettingsUpdated, "presets", map[string]any{"op": "add", "changed": []string{"hero-band"}})
	if rec := g.do("POST", "/admin/theme/presets/hero-band/delete", "owner-token", url.Values{}, nil); rec.Code != 303 {
		t.Fatalf("delete: %d", rec.Code)
	}
	designAuditRow(t, read, 3, AuditSettingsUpdated, "presets", map[string]any{"op": "delete", "changed": []string{"hero-band"}})
}

func TestDesignAuditThemeSettings(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	read := auditCapture(g.a)
	if rec := g.do("POST", "/admin/theme/themes/sunrise/settings", "owner-token", url.Values{"accent": {"bad"}}, nil); rec.Code != 422 {
		t.Fatalf("invalid: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("failed save audited: %+v", evs)
	}
	if rec := g.do("POST", "/admin/theme/themes/sunrise/settings", "owner-token", url.Values{"accent": {"rose"}}, nil); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	designAuditRow(t, read, 1, AuditSettingsUpdated, "theme:sunrise", map[string]any{"changed": []string{"accent"}})
}

func TestDesignAuditSectionTemplates(t *testing.T) {
	t.Parallel()

	tmpl := &sectiontemplate.SectionTemplate{ID: uuid.New(), Name: "Hero", SectionType: "hero"}
	fs := &fakeSections{m: map[uuid.UUID]*sectiontemplate.SectionTemplate{tmpl.ID: tmpl}}
	g := newRig(t, func(d *Deps) { d.Growth.Sections = fs })
	read := auditCapture(g.a)
	u := "/admin/theme/templates/" + tmpl.ID.String()
	if rec := g.do("POST", u, "owner-token", url.Values{"name": {""}}, nil); rec.Code != 422 {
		t.Fatalf("invalid: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("failed save audited: %+v", evs)
	}
	if rec := g.do("POST", u, "owner-token", url.Values{"name": {"Renamed"}}, nil); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	designAuditRow(t, read, 1, AuditTemplateSaved, tmpl.ID.String(), map[string]any{"name": "Renamed"})
	if rec := g.do("POST", u+"/delete", "owner-token", url.Values{}, nil); rec.Code != 303 {
		t.Fatalf("delete: %d", rec.Code)
	}
	designAuditRow(t, read, 2, AuditTemplateDeleted, tmpl.ID.String(), map[string]any{"name": "Renamed"})
}
