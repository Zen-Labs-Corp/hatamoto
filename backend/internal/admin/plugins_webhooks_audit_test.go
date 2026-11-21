package admin

import (
	"net/url"
	"slices"
	"testing"

	"hatamoto.dev/core/internal/agents"
)

const (
	pluginsWebhooksAuditSecret = "typed-plugin-secret-1"
	pluginsWebhooksAuditURL    = "https://hooks.zapier.test/abc"
)

// "plugin.settings" is the action the CLI and REST already write, with "keys".
func init() { auditDetailKeys["plugin.settings"] = []string{"keys"} }

func pluginsWebhooksAuditLast(t *testing.T, read func() []AuditEvent, n int, action, target string) AuditEvent {
	t.Helper()
	evs := read()
	assertAuditSafe(t, evs, pluginsWebhooksAuditSecret, pluginsWebhooksAuditURL, fakeSecret, "whsec_REGENERATED", "X-Team")
	if len(evs) != n {
		t.Fatalf("events = %+v, want %d", evs, n)
	}
	ev := evs[n-1]
	if ev.Action != action || ev.Target != target || ev.Actor != "owner@example.com" {
		t.Fatalf("row = %+v, want %s on %s by the owner", ev, action, target)
	}
	return ev
}

func TestPluginsWebhooksAuditPluginSettingsAndGrant(t *testing.T) {
	t.Parallel()

	g, _ := newPluginRig(t)
	read := auditCapture(g.a)

	if rec := g.do("POST", "/admin/plugins/hello/settings", "owner-token", url.Values{"site_id": {""}}, nil); rec.Code != 422 {
		t.Fatalf("invalid settings: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/plugins/hello/grant", "owner-token", url.Values{"permission": {"entities.write"}}, nil); rec.Code != 400 {
		t.Fatalf("invalid grant: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("failed saves audited: %+v", evs)
	}

	form := url.Values{"site_id": {"xyz"}, "api_key": {pluginsWebhooksAuditSecret}}
	if rec := g.do("POST", "/admin/plugins/hello/settings", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("settings: %d", rec.Code)
	}
	ev := pluginsWebhooksAuditLast(t, read, 1, "plugin.settings", "plugin:hello")
	if keys, _ := ev.Detail["keys"].([]string); !slices.Equal(keys, []string{"api_key", "site_id"}) {
		t.Fatalf("detail = %v", ev.Detail)
	}

	if rec := g.do("POST", "/admin/plugins/hello/grant", "owner-token", url.Values{"permission": {"kv", "mail.send"}}, nil); rec.Code != 303 {
		t.Fatalf("grant: %d", rec.Code)
	}
	// The grant itself records "package.grants" in the packages manager; the
	// admin handler must not add a second row for the same save.
	if evs := read(); len(evs) != 1 {
		t.Fatalf("grant wrote an extra admin row: %+v", evs)
	}
}

func TestPluginsWebhooksAuditRobots(t *testing.T) {
	t.Parallel()

	g, fs := newAnalyticsRig(t)
	read := auditCapture(g.a)
	if rec := g.do("POST", "/admin/analytics/crawlers/robots", "owner-token", url.Values{"purpose.training": {"maybe"}}, nil); rec.Code != 400 {
		t.Fatalf("invalid: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("failed save audited: %+v", evs)
	}
	fs.denied = []agents.Purpose{agents.PurposeUserFetch}
	form := url.Values{}
	for _, p := range robotsPurposes {
		form.Set("purpose."+string(p), map[bool]string{true: "deny", false: "allow"}[p == agents.PurposeUserFetch || p == agents.PurposeTraining])
	}
	if rec := g.do("POST", "/admin/analytics/crawlers/robots", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	ev := pluginsWebhooksAuditLast(t, read, 1, AuditSettingsUpdated, "robots")
	if changed, _ := ev.Detail["changed"].([]string); !slices.Equal(changed, []string{string(agents.PurposeTraining)}) {
		t.Fatalf("detail = %v", ev.Detail)
	}
}

func TestPluginsWebhooksAuditWebhooks(t *testing.T) {
	t.Parallel()

	g, f := newWebhookRig(t)
	read := auditCapture(g.a)
	zapier := f.subs[0].ID

	if rec := g.do("POST", "/admin/settings/webhooks", "owner-token", url.Values{"name": {""}}, nil); rec.Code != 422 {
		t.Fatalf("invalid create: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/settings/webhooks/"+zapier, "owner-token", url.Values{"name": {""}}, nil); rec.Code != 422 {
		t.Fatalf("invalid update: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("failed saves audited: %+v", evs)
	}

	if rec := g.do("POST", "/admin/settings/webhooks", "owner-token", webhookForm1, nil); rec.Code != 303 {
		t.Fatalf("create: %d", rec.Code)
	}
	created := f.created[0].ID
	ev := pluginsWebhooksAuditLast(t, read, 1, AuditWebhookCreated, created)
	if events, _ := ev.Detail["events"].([]string); ev.Detail["name"] != "Zapier" || !slices.Equal(events, []string{"entity.published", "form.submitted"}) {
		t.Fatalf("detail = %v", ev.Detail)
	}

	if rec := g.do("POST", "/admin/settings/webhooks/"+zapier, "owner-token", webhookForm1, nil); rec.Code != 303 {
		t.Fatalf("update: %d", rec.Code)
	}
	ev = pluginsWebhooksAuditLast(t, read, 2, AuditWebhookUpdated, zapier)
	changed, _ := ev.Detail["changed"].([]string)
	if !slices.Contains(changed, "events") || slices.Contains(changed, "name") || slices.Contains(changed, "url") {
		t.Fatalf("changed = %v", changed)
	}

	for i, step := range []struct{ path, action string }{
		{"/pause", AuditWebhookPaused}, {"/resume", AuditWebhookResumed}, {"/new-secret", AuditWebhookSecretRot}, {"/delete", AuditWebhookDeleted},
	} {
		if rec := g.do("POST", "/admin/settings/webhooks/"+zapier+step.path, "owner-token", nil, nil); rec.Code != 303 {
			t.Fatalf("%s: %d", step.path, rec.Code)
		}
		ev = pluginsWebhooksAuditLast(t, read, 3+i, step.action, zapier)
		if ev.Detail["name"] != "Zapier" {
			t.Errorf("%s: detail = %v", step.action, ev.Detail)
		}
	}
}
