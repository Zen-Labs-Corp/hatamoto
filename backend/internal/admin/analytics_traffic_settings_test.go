package admin

import (
	"context"
	"errors"
	"net/url"
	"testing"
)

func settingsRig(t *testing.T, eng *fakeEngagement) (*rig, *[]AuditEvent) {
	t.Helper()
	var events []AuditEvent
	g := trafficRig(t, func(d *Deps) {
		d.Growth.EngagementSetting = eng
		d.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }
	})
	return g, &events
}

func TestTrafficSettingsOwnerFlipsEngagement(t *testing.T) {
	t.Parallel()

	eng := &fakeEngagement{on: true}
	g, events := settingsRig(t, eng)
	form := url.Values{"return": {"/admin/analytics/traffic?from=2026-01-01&to=2026-01-31&compare=1&evil=x"}}
	rec := g.do("POST", "/admin/analytics/traffic/settings", "owner-token", form, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/analytics/traffic?compare=1&from=2026-01-01&to=2026-01-31" {
		t.Fatalf("status %d, location %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(eng.set) != 1 || eng.set[0] {
		t.Errorf("an unchecked switch posts nothing and turns engagement off: %v", eng.set)
	}
	if len(*events) != 1 || (*events)[0].Action != "analytics.engagement" || (*events)[0].Detail["enabled"] != false {
		t.Errorf("audit = %+v", *events)
	}

	form.Set("engagement", "1")
	if rec := g.do("POST", "/admin/analytics/traffic/settings", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("status %d", rec.Code)
	}
	if !eng.on || len(eng.set) != 2 || !eng.set[1] {
		t.Errorf("value not persisted through the setter: %v", eng.set)
	}
	if len(*events) != 2 || (*events)[1].Detail["enabled"] != true {
		t.Errorf("audit = %+v", *events)
	}
}

func TestTrafficSettingsReturnCannotLeaveThePage(t *testing.T) {
	t.Parallel()

	g, _ := settingsRig(t, &fakeEngagement{on: true})
	rec := g.do("POST", "/admin/analytics/traffic/settings", "owner-token", url.Values{"return": {"https://evil.example/x?from=2026-01-01"}}, nil)
	if loc := rec.Header().Get("Location"); loc != "/admin/analytics/traffic?from=2026-01-01" {
		t.Errorf("location %q", loc)
	}
}

func TestTrafficSettingsEditorForbiddenAndCSRFRequired(t *testing.T) {
	t.Parallel()

	eng := &fakeEngagement{on: true}
	g, events := settingsRig(t, eng)
	if rec := g.do("POST", "/admin/analytics/traffic/settings", "editor-token", url.Values{}, nil); rec.Code != 403 {
		t.Errorf("editor = %d, want 403", rec.Code)
	}
	if rec := g.do("POST", "/admin/analytics/traffic/settings", "owner-token", url.Values{}, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
		t.Errorf("no CSRF = %d, want 403", rec.Code)
	}
	if len(eng.set) != 0 || len(*events) != 0 {
		t.Errorf("a refused post changed %v and audited %v", eng.set, *events)
	}
}

func TestTrafficSettingsFailureAndMissingSetting(t *testing.T) {
	t.Parallel()

	g, events := settingsRig(t, &fakeEngagement{on: true, err: errors.New("boom")})
	if rec := g.do("POST", "/admin/analytics/traffic/settings", "owner-token", url.Values{}, nil); rec.Code != 500 {
		t.Errorf("failing store = %d, want 500", rec.Code)
	}
	if len(*events) != 0 {
		t.Error("a failed save is not audited")
	}
	if rec := trafficRig(t).do("POST", "/admin/analytics/traffic/settings", "owner-token", url.Values{}, nil); rec.Code != 503 {
		t.Errorf("no setting wired = %d, want 503", rec.Code)
	}
}
