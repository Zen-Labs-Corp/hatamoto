package admin

import (
	"net/url"
	"slices"
	"testing"

	"github.com/google/uuid"
)

func schemasLinkingAuditLast(t *testing.T, read func() []AuditEvent, n int, action, target string) AuditEvent {
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
	return ev
}

func TestSchemasLinkingAuditContentTypes(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	read := auditCapture(c.a)

	if rec := c.post("/admin/settings/types", url.Values{"name": {"Broken"}, "op": {"save_json"}, "schema_json": {`{"type":`}}); rec.Code != 422 {
		t.Fatalf("invalid create: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("failed create audited: %+v", evs)
	}

	rec := c.post("/admin/settings/types", url.Values{"name": {"Testimonial"}, "field": {"r0"}, "f.r0.name": {"quote"}, "f.r0.kind": {"textarea"}, "f.r0.title": {"Quote"}})
	if rec.Code != 303 {
		t.Fatalf("create: %d", rec.Code)
	}
	ev := schemasLinkingAuditLast(t, read, 1, AuditContentTypeCreated, "testimonial")
	if ev.Detail["name"] != "Testimonial" {
		t.Fatalf("detail = %v", ev.Detail)
	}

	id := fid(1).String()
	rec = c.post("/admin/settings/types/"+id, schemaPost(c, func(f url.Values) { f.Set("description", "Changed text") }))
	if rec.Code != 303 {
		t.Fatalf("update: %d", rec.Code)
	}
	ev = schemasLinkingAuditLast(t, read, 2, AuditContentTypeUpdated, c.service.Slug)
	if changed, _ := ev.Detail["changed"].([]string); !slices.Contains(changed, "description") || slices.Contains(changed, "name") {
		t.Fatalf("detail = %v", ev.Detail)
	}

	// An in-use type is refused: no row.
	if rec := c.post("/admin/settings/types/"+id+"/delete", nil); rec.Code != 303 {
		t.Fatalf("in-use delete: %d", rec.Code)
	}
	if evs := read(); len(evs) != 2 {
		t.Fatalf("refused delete audited: %+v", evs)
	}
	if rec := c.post("/admin/settings/types/"+fid(3).String()+"/delete", nil); rec.Code != 303 {
		t.Fatalf("delete: %d", rec.Code)
	}
	ev = schemasLinkingAuditLast(t, read, 3, AuditContentTypeDeleted, "recipe")
	if ev.Detail["name"] != "Recipe" {
		t.Fatalf("detail = %v", ev.Detail)
	}
}

func TestSchemasLinkingAuditRules(t *testing.T) {
	t.Parallel()

	g, fl, target, source := newLinkRig(t)
	read := auditCapture(g.a)

	if rec := g.do("POST", "/admin/settings/linking/rules", "owner-token", url.Values{"phrase": {""}}, nil); rec.Code != 422 {
		t.Fatalf("invalid rule: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("failed save audited: %+v", evs)
	}

	good := url.Values{"phrase": {"web design"}, "target": {target.String()}, "target_prev": {"Services"}, "priority": {"80"}, "maxlinks": {"2"}, "active": {"1"}}
	if rec := g.do("POST", "/admin/settings/linking/rules", "owner-token", good, nil); rec.Code != 303 {
		t.Fatalf("create: %d", rec.Code)
	}
	var id uuid.UUID
	for k := range fl.rules {
		id = k
	}
	ev := schemasLinkingAuditLast(t, read, 1, AuditLinkRuleSaved, id.String())
	if ev.Detail["enabled"] != true {
		t.Fatalf("detail = %v", ev.Detail)
	}
	u := "/admin/settings/linking/rules/" + id.String()
	good.Del("active")
	if rec := g.do("POST", u, "owner-token", good, nil); rec.Code != 303 {
		t.Fatalf("update: %d", rec.Code)
	}
	ev = schemasLinkingAuditLast(t, read, 2, AuditLinkRuleSaved, id.String())
	if ev.Detail["enabled"] != false {
		t.Fatalf("detail = %v", ev.Detail)
	}
	g.do("POST", u+"/toggle", "owner-token", url.Values{}, nil)
	ev = schemasLinkingAuditLast(t, read, 3, AuditLinkRuleToggled, id.String())
	if ev.Detail["enabled"] != true {
		t.Fatalf("detail = %v", ev.Detail)
	}
	g.do("POST", u+"/delete", "owner-token", url.Values{}, nil)
	schemasLinkingAuditLast(t, read, 4, AuditLinkRuleDeleted, id.String())

	// Bulk operations write one row each, with the count.
	a, b := fl.add("a", target, "manual"), fl.add("b", target, "manual")
	fl.add("c", target, "auto_tag")
	g.do("POST", "/admin/settings/linking/rules/bulk", "owner-token", url.Values{"action": {"delete"}, "id": {a.ID.String(), b.ID.String()}}, nil)
	if evs := read(); len(evs) != 4 {
		t.Fatalf("the confirmation page audited: %+v", evs)
	}
	g.do("POST", "/admin/settings/linking/rules/bulk", "owner-token", url.Values{"action": {"delete"}, "confirm": {"1"}, "id": {a.ID.String(), b.ID.String()}}, nil)
	ev = schemasLinkingAuditLast(t, read, 5, AuditLinkRulesChanged, "linking")
	if ev.Detail["op"] != "bulk" || ev.Detail["count"] != 2 {
		t.Fatalf("detail = %v", ev.Detail)
	}
	g.do("POST", "/admin/settings/linking/rules/delete-source", "owner-token", url.Values{"source": {"auto_tag"}, "confirm": {"1"}}, nil)
	ev = schemasLinkingAuditLast(t, read, 6, AuditLinkRulesChanged, "linking")
	if ev.Detail["op"] != "delete_source" || ev.Detail["count"] != 1 {
		t.Fatalf("detail = %v", ev.Detail)
	}
	fl.add("z", target, "manual")
	g.do("POST", "/admin/settings/linking/rules/delete-all", "owner-token", url.Values{}, nil)
	ev = schemasLinkingAuditLast(t, read, 7, AuditLinkRulesChanged, "linking")
	if ev.Detail["op"] != "delete_all" || ev.Detail["count"] != 1 {
		t.Fatalf("detail = %v", ev.Detail)
	}
	g.do("POST", "/admin/settings/linking/rules/delete-all", "owner-token", url.Values{}, nil)
	if evs := read(); len(evs) != 7 {
		t.Fatalf("deleting nothing audited: %+v", evs)
	}

	if rec := g.do("POST", "/admin/settings/linking/import", "owner-token", url.Values{"rules": {""}}, nil); rec.Code != 422 {
		t.Fatalf("empty import: %d", rec.Code)
	}
	g.do("POST", "/admin/settings/linking/import", "owner-token", url.Values{"rules": {"web design, services, 70\nmissing, nothing-here"}}, nil)
	ev = schemasLinkingAuditLast(t, read, 8, AuditLinkRulesChanged, "linking")
	if ev.Detail["op"] != "import" || ev.Detail["count"] != 1 {
		t.Fatalf("detail = %v", ev.Detail)
	}

	ins := url.Values{"source": {source.String()}, "target": {target.String()}, "anchor": {"our services"}, "label": {"Home page"}}
	if rec := g.do("POST", "/admin/settings/linking/suggestions/insert", "owner-token", ins, nil); rec.Code != 303 {
		t.Fatalf("insert: %d", rec.Code)
	}
	ev = schemasLinkingAuditLast(t, read, 9, AuditEntityLinksInserted, source.String())
	if ev.Detail["count"] != 1 {
		t.Fatalf("detail = %v", ev.Detail)
	}
	// A skipped insert changed nothing: no row.
	fl.inserted = []InsertOutcome{{SkipReason: "already_linked"}}
	g.do("POST", "/admin/settings/linking/suggestions/insert", "owner-token", ins, nil)
	if evs := read(); len(evs) != 9 {
		t.Fatalf("skipped insert audited: %+v", evs)
	}
}
