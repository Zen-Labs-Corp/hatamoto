package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

// fakePublicAPI is a PublicAPISettings that records the saves.
type fakePublicAPI struct {
	on    bool
	saved []bool
}

func (f *fakePublicAPI) Enabled(context.Context) bool { return f.on }
func (f *fakePublicAPI) SetEnabled(_ context.Context, on bool) error {
	f.saved = append(f.saved, on)
	f.on = on
	return nil
}

const publicAPISettingsURL = "/admin/settings/public-api"

func newPublicAPIRig(t *testing.T, svc *fakePublicAPI) *perfRig {
	t.Helper()
	p := newPerfRig(t, nil)
	if svc != nil {
		p.a.deps.PublicAPI = svc
	}
	return p
}

func TestPublicAPISettingsPage(t *testing.T) {
	t.Parallel()

	p := newPublicAPIRig(t, &fakePublicAPI{on: true})
	rec := p.do("GET", publicAPISettingsURL, "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	for _, want := range []string{`name="public_content_api"`, `role="switch"`, ` checked`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestPublicAPISettingsSave(t *testing.T) {
	t.Parallel()

	svc := &fakePublicAPI{}
	p := newPublicAPIRig(t, svc)
	rec := p.do("POST", publicAPISettingsURL, "owner-token", url.Values{"public_content_api": {"1"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != publicAPISettingsURL {
		t.Fatalf("save = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(svc.saved) != 1 || !svc.saved[0] {
		t.Fatalf("saved = %v", svc.saved)
	}
	if len(p.events) != 1 || p.events[0].Action != "settings.public_api" || p.events[0].Detail["enabled"] != true {
		t.Fatalf("audit = %+v", p.events)
	}

	// The off value posted by the switch turns it off again.
	if rec := p.do("POST", publicAPISettingsURL, "owner-token", url.Values{"public_content_api": {"0"}}, nil); rec.Code != 303 {
		t.Fatalf("off = %d", rec.Code)
	}
	if len(svc.saved) != 2 || svc.saved[1] {
		t.Errorf("saved = %v, want on then off", svc.saved)
	}
}

func TestPublicAPISettingsOwnerOnly(t *testing.T) {
	t.Parallel()

	svc := &fakePublicAPI{}
	p := newPublicAPIRig(t, svc)
	if rec := p.do("GET", publicAPISettingsURL, "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d", rec.Code)
	}
	if rec := p.do("POST", publicAPISettingsURL, "editor-token", url.Values{"public_content_api": {"1"}}, nil); rec.Code != 403 {
		t.Errorf("editor POST = %d", rec.Code)
	}
	if len(svc.saved) != 0 || len(p.events) != 0 {
		t.Error("a refused request reached the service")
	}
}

func TestPublicAPISettingsUnavailable(t *testing.T) {
	t.Parallel()

	p := newPublicAPIRig(t, nil)
	if rec := p.do("GET", publicAPISettingsURL, "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("GET without service = %d", rec.Code)
	}
}

func TestPublicAPISettingsPageGolden(t *testing.T) {
	c := reviewGoldenRig(t)
	c.a.deps.PublicAPI = &fakePublicAPI{}
	compareGolden(t, "pages/settings-public-api.html", normalizePage(c.get(t, "owner-token", "/admin/settings/public-api", "")))
}
