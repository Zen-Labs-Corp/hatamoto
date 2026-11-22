package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/entity"
)

// fakeReviewFlow is a ReviewFlow with the switch and canned answers.
type fakeReviewFlow struct {
	on      bool
	saved   []bool
	held    bool
	pending map[string]PendingEdit
}

func (f *fakeReviewFlow) Enabled(context.Context) bool { return f.on }
func (f *fakeReviewFlow) SetEnabled(_ context.Context, on bool) error {
	f.saved = append(f.saved, on)
	f.on = on
	return nil
}
func (f *fakeReviewFlow) Plan(context.Context, *entity.Entity) bool { return f.held }
func (f *fakeReviewFlow) Pending(_ context.Context, id string) (PendingEdit, bool, error) {
	p, ok := f.pending[id]
	return p, ok, nil
}

const reviewSettingsURL = "/admin/settings/review"

func newReviewSettingsRig(t *testing.T, svc *fakeReviewFlow) *perfRig {
	t.Helper()
	p := newPerfRig(t, nil)
	p.a.deps.Review = svc
	return p
}

func TestReviewSettingsPage(t *testing.T) {
	t.Parallel()

	p := newReviewSettingsRig(t, &fakeReviewFlow{on: true})
	rec := p.do("GET", reviewSettingsURL, "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	for _, want := range []string{`name="review_enabled"`, `role="switch"`, ` checked`, "Owners and reviewers publish directly."} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestReviewSettingsSave(t *testing.T) {
	t.Parallel()

	svc := &fakeReviewFlow{}
	p := newReviewSettingsRig(t, svc)
	rec := p.do("POST", reviewSettingsURL, "owner-token", url.Values{"review_enabled": {"1"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != reviewSettingsURL {
		t.Fatalf("save = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(svc.saved) != 1 || !svc.saved[0] {
		t.Fatalf("saved = %v", svc.saved)
	}
	if len(p.events) != 1 || p.events[0].Action != "settings.review" || p.events[0].Detail["enabled"] != true {
		t.Fatalf("audit = %+v", p.events)
	}

	// The off value posted by the switch turns it off again.
	if rec := p.do("POST", reviewSettingsURL, "owner-token", url.Values{"review_enabled": {"0"}}, nil); rec.Code != 303 {
		t.Fatalf("off = %d", rec.Code)
	}
	if len(svc.saved) != 2 || svc.saved[1] {
		t.Errorf("saved = %v, want on then off", svc.saved)
	}
}

func TestReviewSettingsOwnerOnly(t *testing.T) {
	t.Parallel()

	svc := &fakeReviewFlow{}
	p := newReviewSettingsRig(t, svc)
	if rec := p.do("GET", reviewSettingsURL, "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d", rec.Code)
	}
	if rec := p.do("POST", reviewSettingsURL, "editor-token", url.Values{"review_enabled": {"1"}}, nil); rec.Code != 403 {
		t.Errorf("editor POST = %d", rec.Code)
	}
	if len(svc.saved) != 0 || len(p.events) != 0 {
		t.Error("a refused request reached the service")
	}
}

func TestReviewSettingsUnavailable(t *testing.T) {
	t.Parallel()

	p := newPerfRig(t, nil)
	if rec := p.do("GET", reviewSettingsURL, "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("GET without service = %d", rec.Code)
	}
}
