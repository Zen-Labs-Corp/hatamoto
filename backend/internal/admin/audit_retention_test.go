package admin

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

type fakeAuditRetention struct {
	admin, agent int
	err          error
	saved        [][2]int
}

func (f *fakeAuditRetention) Days(context.Context) (int, int, error) { return f.admin, f.agent, f.err }
func (f *fakeAuditRetention) SetDays(_ context.Context, admin, agent int) error {
	f.saved = append(f.saved, [2]int{admin, agent})
	f.admin, f.agent = admin, agent
	return nil
}

const auditRetentionURL = "/admin/audit/retention"

func newAuditRetentionRig(t *testing.T, svc *fakeAuditRetention) (*rig, *[]AuditEvent) {
	t.Helper()
	var events []AuditEvent
	g := newRig(t, func(d *Deps) {
		d.AuditLog = &fakeAuditLog{rows: auditRows(1)}
		if svc != nil {
			d.AuditRetention = svc
		}
		d.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }
	})
	return g, &events
}

func TestAuditRetentionPage(t *testing.T) {
	t.Parallel()

	g, _ := newAuditRetentionRig(t, &fakeAuditRetention{admin: 365, agent: 90})
	rec := g.do("GET", auditRetentionURL, "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	for _, want := range []string{`name="keep"`, `name="keep_agent"`, `<option value="365" selected>1 year`, `<option value="90" selected>90 days`, "daily cleanup"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	compareGolden(t, "pages/audit-retention.html", normalizePage(body))
}

func TestAuditRetentionSave(t *testing.T) {
	t.Parallel()

	svc := &fakeAuditRetention{admin: 365, agent: 90}
	g, events := newAuditRetentionRig(t, svc)
	rec := g.do("POST", auditRetentionURL, "owner-token", url.Values{"keep": {"730"}, "keep_agent": {"180"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != auditRetentionURL {
		t.Fatalf("save = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(svc.saved) != 1 || svc.saved[0] != [2]int{730, 180} {
		t.Fatalf("saved = %v", svc.saved)
	}
	if len(*events) != 1 || (*events)[0].Action != "audit.retention" || (*events)[0].Detail["old"] != 365 || (*events)[0].Detail["new"] != 730 || (*events)[0].Detail["new_agent"] != 180 {
		t.Fatalf("audit = %+v", *events)
	}
	if auditLabel("audit.retention") == "audit.retention" {
		t.Error("audit.retention has no plain label")
	}

	// The same values again change nothing and write no audit row.
	if rec := g.do("POST", auditRetentionURL, "owner-token", url.Values{"keep": {"730"}, "keep_agent": {"180"}}, nil); rec.Code != 303 {
		t.Fatalf("repeat save = %d", rec.Code)
	}
	if len(svc.saved) != 1 || len(*events) != 1 {
		t.Errorf("an unchanged value was saved or audited: %v %d", svc.saved, len(*events))
	}
}

func TestAuditRetentionValidation(t *testing.T) {
	t.Parallel()

	for _, form := range []url.Values{
		{"keep": {"7"}, "keep_agent": {"90"}},
		{"keep": {"365"}, "keep_agent": {"abc"}},
		{"keep": {"365"}},
		{},
	} {
		svc := &fakeAuditRetention{admin: 365, agent: 90}
		g, events := newAuditRetentionRig(t, svc)
		rec := g.do("POST", auditRetentionURL, "owner-token", form, nil)
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Choose one of the listed periods.") {
			t.Errorf("%v: %d, want a field error", form, rec.Code)
		}
		if len(svc.saved) != 0 || len(*events) != 0 {
			t.Errorf("%v: an invalid value was saved or audited", form)
		}
	}
}

func TestAuditRetentionGuards(t *testing.T) {
	t.Parallel()

	svc := &fakeAuditRetention{admin: 365, agent: 90}
	g, _ := newAuditRetentionRig(t, svc)
	form := url.Values{"keep": {"730"}, "keep_agent": {"180"}}
	if rec := g.do("GET", auditRetentionURL, "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d", rec.Code)
	}
	if rec := g.do("POST", auditRetentionURL, "editor-token", form, nil); rec.Code != 403 {
		t.Errorf("editor POST = %d", rec.Code)
	}
	if rec := g.do("POST", auditRetentionURL, "owner-token", form, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
		t.Errorf("POST without a CSRF token = %d", rec.Code)
	}
	if len(svc.saved) != 0 {
		t.Error("a refused request reached the service")
	}

	none, _ := newAuditRetentionRig(t, nil)
	if rec := none.do("GET", auditRetentionURL, "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("GET without a service = %d", rec.Code)
	}
	if rec := none.do("POST", auditRetentionURL, "owner-token", form, nil); rec.Code != 503 {
		t.Errorf("POST without a service = %d", rec.Code)
	}

	failing, _ := newAuditRetentionRig(t, &fakeAuditRetention{err: errors.New("down")})
	if rec := failing.do("GET", auditRetentionURL, "owner-token", nil, nil); rec.Code != 500 {
		t.Errorf("GET with a failing read = %d", rec.Code)
	}
}

func TestAuditLogShowsRetentionLinkOnlyWithAService(t *testing.T) {
	t.Parallel()

	g, _ := newAuditRetentionRig(t, &fakeAuditRetention{admin: 365, agent: 90})
	body := g.do("GET", "/admin/audit", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`href="/admin/audit/retention"`, "Keep entries for", "1 year · agent calls 90 days"} {
		if !strings.Contains(body, want) {
			t.Errorf("audit page lacks %q", want)
		}
	}
	none, _ := newAuditRetentionRig(t, nil)
	if body := none.do("GET", "/admin/audit", "owner-token", nil, nil).Body.String(); strings.Contains(body, "/audit/retention") {
		t.Error("retention link shown without a service")
	}
}
