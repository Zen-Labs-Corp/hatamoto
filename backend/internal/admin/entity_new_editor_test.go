package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

func newDraftRig(t *testing.T) *blogRig {
	r := newBlogRig(t)
	r.ents.addSchema(21, "Page", "page", `{"type":"object","properties":{}}`, `{}`, true)
	r.ents.addSchema(22, "Service", "service", `{"type":"object","properties":{}}`, `{}`, false)
	return r
}

func (r *blogRig) drafts() int {
	n := 0
	for _, e := range r.ents.ents {
		if strings.HasPrefix(e.Name, "Untitled") {
			n++
		}
	}
	return n
}

func TestGetNewPostCreatesNothingAndAsksFirst(t *testing.T) {
	t.Parallel()

	r := newDraftRig(t)
	for _, hdr := range []map[string]string{nil, {"Sec-Purpose": "prefetch"}} {
		rec := r.do("GET", "/admin/entities/new?type=post", "owner-token", nil, hdr)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, `action="/admin/entities/drafts"`) || !strings.Contains(body, "Start a new post") {
			t.Fatalf("confirm page: %d %s", rec.Code, body)
		}
	}
	if n := r.drafts(); n != 0 {
		t.Fatalf("GET created %d drafts", n)
	}
	if rec := r.do("GET", "/admin/entities/new?type=service", "owner-token", nil, nil); !strings.Contains(rec.Body.String(), `name="name"`) {
		t.Error("service no longer gets the generic form")
	}
}

func TestPostDraftCreatesOnceAndReusesTheUntouchedDraft(t *testing.T) {
	t.Parallel()

	r := newDraftRig(t)
	form := url.Values{"type": {"post"}}
	rec := r.do("POST", "/admin/entities/drafts", "owner-token", form, nil)
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.HasSuffix(loc, "/edit") || r.drafts() != 1 {
		t.Fatalf("create: %d %q drafts=%d", rec.Code, loc, r.drafts())
	}
	rec = r.do("POST", "/admin/entities/drafts", "owner-token", form, nil)
	if rec.Header().Get("Location") != loc || r.drafts() != 1 {
		t.Fatalf("second POST: %q (want %q) drafts=%d", rec.Header().Get("Location"), loc, r.drafts())
	}
	// once edited it is no longer untouched
	for _, e := range r.ents.ents {
		if strings.HasPrefix(e.Name, "Untitled") {
			e.Version = 2
		}
	}
	if rec = r.do("POST", "/admin/entities/drafts", "owner-token", form, nil); rec.Header().Get("Location") == loc || r.drafts() != 2 {
		t.Errorf("edited draft was reused: %q drafts=%d", rec.Header().Get("Location"), r.drafts())
	}
	if rec = r.do("GET", strings.TrimPrefix(r.do("POST", "/admin/entities/drafts", "owner-token", form, nil).Header().Get("Location"), ""), "owner-token", nil, nil); rec.Code != http.StatusOK {
		t.Errorf("editor does not open: %d", rec.Code)
	}
}

func TestPostDraftForPageAndRejections(t *testing.T) {
	t.Parallel()

	r := newDraftRig(t)
	rec := r.do("POST", "/admin/entities/drafts", "owner-token", url.Values{"type": {"page"}}, nil)
	if rec.Code != http.StatusSeeOther || r.drafts() != 1 {
		t.Fatalf("page: %d drafts=%d", rec.Code, r.drafts())
	}
	for _, e := range r.ents.ents {
		if e.Name == "Untitled page" && e.Status != entity.StatusDraft {
			t.Error("page draft is not a draft")
		}
	}
	if rec = r.do("POST", "/admin/entities/drafts", "owner-token", url.Values{"type": {"service"}}, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("service: %d", rec.Code)
	}
	if rec = r.do("POST", "/admin/entities/drafts", "", url.Values{"type": {"post"}}, nil); rec.Code == http.StatusSeeOther && strings.HasSuffix(rec.Header().Get("Location"), "/edit") {
		t.Error("anonymous request created a draft")
	}
}

func TestCreateMenuPostsForPostsAndPages(t *testing.T) {
	t.Parallel()

	r := newDraftRig(t)
	body := r.do("GET", "/admin/entities", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `action="/admin/entities/drafts"`) || !strings.Contains(body, `name="type" value="post"`) || strings.Contains(body, "new?type=post") {
		t.Error("the Create menu must POST for posts")
	}
	if !strings.Contains(body, "new?type=service") {
		t.Error("other types keep their link")
	}
}
