package admin

import (
	"net/url"
	"strings"
	"testing"
)

// A new page opens the builder on its own start panel, so the "Start writing below" flash is not set;
// the post editor still gets it.
func TestEntityDraftNoFlashForBuilderPages(t *testing.T) {
	t.Parallel()

	r := newDraftRig(t)
	rec := r.do("POST", "/admin/entities/drafts", "owner-token", url.Values{"type": {"page"}}, nil)
	if rec.Code != 303 || !strings.HasSuffix(rec.Header().Get("Location"), "/edit") {
		t.Fatalf("page draft: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if hasFlash(rec) {
		t.Error("a page draft sets a flash; the builder's start panel replaces it")
	}
}

func TestEntityDraftKeepsFlashForOtherTypes(t *testing.T) {
	t.Parallel()

	r := newDraftRig(t)
	rec := r.do("POST", "/admin/entities/drafts", "owner-token", url.Values{"type": {"post"}}, nil)
	if rec.Code != 303 || !hasFlash(rec) {
		t.Errorf("post draft: %d flash=%v, want 303 and a flash", rec.Code, hasFlash(rec))
	}
}

// Every in-app way to start a page or post is one POST: the chooser no longer links to the GET confirm page.
func TestEntityChooserPostsForDraftTypes(t *testing.T) {
	t.Parallel()

	r := newDraftRig(t)
	body := r.do("GET", "/admin/entities/new", "owner-token", nil, nil).Body.String()
	for _, slug := range []string{"page", "post"} {
		if !strings.Contains(body, `name="type" value="`+slug+`"`) || strings.Contains(body, "new?type="+slug) {
			t.Errorf("the chooser must POST for %s", slug)
		}
	}
	if !strings.Contains(body, `action="/admin/entities/drafts"`) || !strings.Contains(body, "new?type=service") {
		t.Error("draft types POST to /entities/drafts and other types keep their link")
	}
	a11yOK(t, body)
}
