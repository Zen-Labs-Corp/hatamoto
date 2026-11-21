package admin

import (
	"net/url"
	"testing"
)

// The fake store mints this secret; it is split so no scanner reads it as a credential.
const apiKeysAuditMinted = "hm_ab12_" + "THE-ONLY-SECRET"

func TestApiKeysAuditRoutes(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	read := auditCapture(g.a)
	one := func(n int, action, target string) AuditEvent {
		t.Helper()
		evs := read()
		assertAuditSafe(t, evs, apiKeysAuditMinted, "hm_ab12_"+"REGENERATED")
		if len(evs) != n || evs[n-1].Action != action || evs[n-1].Target != target || evs[n-1].Actor != "owner@example.com" {
			t.Fatalf("events = %+v, want %d ending in %s on %s", evs, n, action, target)
		}
		return evs[n-1]
	}

	// Validation failure: no row.
	if rec := g.do("POST", "/admin/settings/api-keys", "owner-token", url.Values{"name": {""}}, nil); rec.Code != 422 {
		t.Fatalf("invalid create: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("failed create audited: %+v", evs)
	}

	form := url.Values{"name": {"CI"}, "scopes": {"read:entities"}, "expiry": {"30"}}
	if rec := g.do("POST", "/admin/settings/api-keys", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("create: %d", rec.Code)
	}
	var newID string
	for id, k := range g.keys.keys {
		if k.Name == "CI" {
			newID = id.String()
		}
	}
	ev := one(1, AuditAPIKeyCreated, "api_key:"+newID)
	scopes, _ := ev.Detail["scopes"].([]string)
	if ev.Detail["name"] != "CI" || ev.Detail["prefix"] != "hm_ab12" || len(scopes) != 1 || scopes[0] != "read:entities" || ev.Detail["expires"] == nil {
		t.Fatalf("detail = %v", ev.Detail)
	}

	base := "/admin/settings/api-keys/" + g.keyID.String()
	g.do("POST", base+"/regenerate", "owner-token", url.Values{}, nil)
	one(2, AuditAPIKeyRegenerated, "api_key:"+g.keyID.String())
	g.do("POST", base+"/revoke", "owner-token", url.Values{}, nil)
	one(3, AuditAPIKeyRevoked, "api_key:"+g.keyID.String())
	g.do("POST", base+"/delete", "owner-token", url.Values{}, nil)
	one(4, AuditAPIKeyDeleted, "api_key:"+g.keyID.String())
}
