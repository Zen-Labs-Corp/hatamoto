package admin

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestAuditNilSinkIsNoOp(t *testing.T) {
	t.Parallel()

	a := &Admin{}
	a.audit(httptest.NewRequest("POST", "/", nil), "cache.clear", "page-cache", nil)
}

func TestAuditCarriesActor(t *testing.T) {
	t.Parallel()

	var got []AuditEvent
	a := &Admin{deps: Deps{Audit: func(_ context.Context, e AuditEvent) { got = append(got, e) }}}
	r := httptest.NewRequest("POST", "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), userKey, &CurrentUser{ID: "u-1", Email: "o@example.com", Role: "owner"}))
	a.audit(r, "cache.clear", "page-cache", map[string]any{"pages": 3})
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}
	e := got[0]
	if e.ActorID != "u-1" || e.Actor != "o@example.com" || e.Action != "cache.clear" || e.Target != "page-cache" || e.Detail["pages"] != 3 {
		t.Fatalf("event = %+v", e)
	}
	// No user on the request still records the action.
	a.audit(httptest.NewRequest("POST", "/", nil), "cache.clear", "page-cache", nil)
	if len(got) != 2 || got[1].Actor != "" {
		t.Fatalf("anonymous event = %+v", got)
	}
}
