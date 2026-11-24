package admin

import (
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

// The a11y tests below cover this page (TestEveryPageTemplateIsExercised).
func init() {
	areaPages["kits"] = []string{"settings/kits"}
}

func newKitsRig(t *testing.T, svc *fakeKits) *perfRig {
	t.Helper()
	p := newPerfRig(t, nil)
	if svc != nil {
		p.a.deps.Kits = svc
	}
	return p
}

func TestKitsListShowsAppliedAndAvailable(t *testing.T) {
	t.Parallel()

	p := newKitsRig(t, kitsFixture())
	rec := p.do("GET", "/admin/settings/kits", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	for _, want := range []string{"Applied", "Available from the registry", "Community site", "v1.0.0", "applied 24 Nov 2026 by owner@example.org", "Journal", "signed: first-party", "signed: community", `href="/admin/settings/kits/journal"`, ">Review<"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "<table") {
		t.Error("the list is a table; Review must be a link row, not a table with row actions")
	}
}

func TestKitsListRegistryUnavailable(t *testing.T) {
	t.Parallel()

	f := kitsFixture()
	f.available, f.listErr = nil, errors.New("offline")
	p := newKitsRig(t, f)
	rec := p.do("GET", "/admin/settings/kits", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "The registry is unavailable.") || !strings.Contains(body, "Community site") {
		t.Errorf("want the unavailable text and the applied kit:\n%s", body)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestKitsListEmpty(t *testing.T) {
	t.Parallel()

	p := newKitsRig(t, &fakeKits{})
	body := p.do("GET", "/admin/settings/kits", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"No kit applied yet", "No kits listed"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestKitsListAuthz(t *testing.T) {
	t.Parallel()

	p := newKitsRig(t, kitsFixture())
	if rec := p.do("GET", "/admin/settings/kits", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d, want 403", rec.Code)
	}
	if rec := p.do("GET", "/admin/settings/kits", "", nil, nil); rec.Code != 303 {
		t.Errorf("anonymous GET = %d, want login redirect", rec.Code)
	}
	q := newKitsRig(t, nil)
	if rec := q.do("GET", "/admin/settings/kits", "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("no service GET = %d, want 503", rec.Code)
	}
}
