package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

type fakeVersionRetention struct {
	keep  int
	saved []int
}

func (f *fakeVersionRetention) Keep(context.Context) (int, error) { return f.keep, nil }
func (f *fakeVersionRetention) SetKeep(_ context.Context, n int) error {
	f.saved = append(f.saved, n)
	f.keep = n
	return nil
}

func newVersionRetentionRig(t *testing.T, svc *fakeVersionRetention) *perfRig {
	t.Helper()
	p := newPerfRig(t, nil)
	p.a.deps.VersionRetention = svc
	return p
}

const versionRetentionURL = "/admin/settings/versions"

func TestVersionRetentionPage(t *testing.T) {
	t.Parallel()

	p := newVersionRetentionRig(t, &fakeVersionRetention{keep: 50})
	rec := p.do("GET", versionRetentionURL, "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	for _, want := range []string{`value="50"`, `min="10"`, `max="500"`, "last 30 days"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	compareGolden(t, "pages/settings-versions.html", normalizePage(body))
}

func TestVersionRetentionSave(t *testing.T) {
	t.Parallel()

	svc := &fakeVersionRetention{keep: 20}
	p := newVersionRetentionRig(t, svc)
	rec := p.do("POST", versionRetentionURL, "owner-token", url.Values{"keep": {" 80 "}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != versionRetentionURL {
		t.Fatalf("save = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(svc.saved) != 1 || svc.saved[0] != 80 {
		t.Fatalf("saved = %v", svc.saved)
	}
	if len(p.events) != 1 || p.events[0].Action != "entity-versions.limit" || p.events[0].Detail["old"] != 20 || p.events[0].Detail["new"] != 80 {
		t.Fatalf("audit = %+v", p.events)
	}
	if body := p.do("GET", versionRetentionURL, "owner-token", nil, nil).Body.String(); !strings.Contains(body, `value="80"`) {
		t.Error("the saved value is not shown again")
	}

	// The same value again changes nothing and writes no audit row.
	if rec := p.do("POST", versionRetentionURL, "owner-token", url.Values{"keep": {"80"}}, nil); rec.Code != 303 {
		t.Fatalf("repeat save = %d", rec.Code)
	}
	if len(svc.saved) != 1 || len(p.events) != 1 {
		t.Errorf("an unchanged value was saved or audited: %v %d", svc.saved, len(p.events))
	}
}

func TestVersionRetentionValidation(t *testing.T) {
	t.Parallel()

	for _, v := range []string{"9", "501", "abc", "", "1.5"} {
		svc := &fakeVersionRetention{keep: 50}
		p := newVersionRetentionRig(t, svc)
		rec := p.do("POST", versionRetentionURL, "owner-token", url.Values{"keep": {v}}, nil)
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Enter a number from 10 to 500.") {
			t.Errorf("%q: %d, want a field error", v, rec.Code)
		}
		if len(svc.saved) != 0 || len(p.events) != 0 {
			t.Errorf("%q: an invalid value was saved or audited", v)
		}
	}
}

func TestVersionRetentionOwnerOnly(t *testing.T) {
	t.Parallel()

	svc := &fakeVersionRetention{keep: 50}
	p := newVersionRetentionRig(t, svc)
	if rec := p.do("GET", versionRetentionURL, "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d", rec.Code)
	}
	if rec := p.do("POST", versionRetentionURL, "editor-token", url.Values{"keep": {"80"}}, nil); rec.Code != 403 {
		t.Errorf("editor POST = %d", rec.Code)
	}
	if len(svc.saved) != 0 || len(p.events) != 0 {
		t.Error("a refused request reached the service")
	}
}

func TestVersionRetentionUnavailable(t *testing.T) {
	t.Parallel()

	p := newPerfRig(t, nil)
	if rec := p.do("GET", versionRetentionURL, "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("GET without service = %d", rec.Code)
	}
}
