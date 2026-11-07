package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/form"
)

func TestFormDuplicate(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	fm.Settings = form.FormSettings{StoreSubmissions: true, SubmitButtonText: "Send", RateLimitPerHour: 5}
	post := func() *httptest.ResponseRecorder {
		return g.do("POST", "/admin/forms/design/"+fm.ID.String()+"/duplicate", "owner-token", url.Values{}, nil)
	}

	rec := post()
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("duplicate: %d %s", rec.Code, rec.Body.String())
	}
	var cp *form.Form
	for _, f := range ff.forms {
		if f.ID != fm.ID {
			cp = f
		}
	}
	if cp == nil || cp.Name != "Copy of Contact" || cp.Status != form.StatusInactive || cp.Slug == fm.Slug {
		t.Fatalf("copy: %+v", cp)
	}
	if len(cp.Fields) != len(fm.Fields) || cp.Settings.RateLimitPerHour != 5 {
		t.Errorf("fields or settings not copied: %+v", cp)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/forms/design/"+cp.ID.String()+"/settings" {
		t.Errorf("location %q", loc)
	}
	if fm.Status != form.StatusActive {
		t.Error("the original changed")
	}

	// A second copy needs another identifier.
	post()
	slugs := map[string]bool{}
	for _, f := range ff.forms {
		if slugs[f.Slug] {
			t.Errorf("slug %q used twice", f.Slug)
		}
		slugs[f.Slug] = true
	}
	if len(ff.forms) != 3 {
		t.Errorf("%d forms, want 3", len(ff.forms))
	}
}

func TestFormDuplicateNameCapAndUnknown(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm(strings.Repeat("é", 120), contactFields...)
	if rec := g.do("POST", "/admin/forms/design/"+fm.ID.String()+"/duplicate", "owner-token", url.Values{}, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("duplicate: %d", rec.Code)
	}
	for _, f := range ff.forms {
		if f.ID != fm.ID && len([]rune(f.Name)) > 120 {
			t.Errorf("copy name has %d characters", len([]rune(f.Name)))
		}
	}
	if rec := g.do("POST", "/admin/forms/design/00000000-0000-0000-0000-000000000000/duplicate", "owner-token", url.Values{}, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown: %d", rec.Code)
	}
}
