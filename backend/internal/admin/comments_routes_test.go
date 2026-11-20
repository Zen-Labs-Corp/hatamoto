package admin

import (
	"net/url"
	"testing"
)

const commentsTestID = "0b6d1d0e-6f3a-4c55-9a3e-6f1d2f6f9a10"

func TestCommentsOwnerRoutesRefuseEditor(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{}
	p := newCommentsRig(t, svc)
	id := commentsTestID
	routes := []struct{ method, path string }{
		{"POST", "/admin/settings/comments"},
		{"POST", "/admin/settings/comments/switch"},
		{"GET", "/admin/settings/comments/enable"},
		{"POST", "/admin/settings/comments/enable"},
		{"POST", "/admin/settings/types/" + id + "/comments"},
		{"GET", "/admin/settings/comments/commenters/" + id + "/export"},
		{"GET", "/admin/settings/comments/commenters/" + id + "/erase"},
		{"POST", "/admin/settings/comments/commenters/" + id + "/erase"},
	}
	for _, rt := range routes {
		var form url.Values
		if rt.method == "POST" {
			form = url.Values{"enabled": {"true"}, "understand": {"1"}}
		}
		if rec := p.do(rt.method, rt.path, "editor-token", form, nil); rec.Code != 403 {
			t.Errorf("editor %s %s = %d, want 403", rt.method, rt.path, rec.Code)
		}
		if rec := p.do(rt.method, rt.path, "", form, nil); rec.Code != 303 {
			t.Errorf("anonymous %s %s = %d, want redirect to login", rt.method, rt.path, rec.Code)
		}
		if rt.method == "POST" {
			if rec := p.do(rt.method, rt.path, "owner-token", form, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
				t.Errorf("no CSRF %s %s = %d, want 403", rt.method, rt.path, rec.Code)
			}
		}
	}
	if len(svc.calls) != 0 || len(p.events) != 0 {
		t.Errorf("a refused request reached the service: calls=%v events=%d", svc.calls, len(p.events))
	}
}

func TestCommentsEditorRoutesReachable(t *testing.T) {
	t.Parallel()

	// The comment exists, so a 404 means the route is not mounted.
	p := newCommentsRig(t, &fakeComments{rows: []CommentRow{{ID: commentsTestID}}})
	id := commentsTestID
	routes := []struct{ method, path string }{
		{"GET", "/admin/comments"},
		{"POST", "/admin/comments/bulk"},
		{"POST", "/admin/comments/" + id + "/approve"},
		{"GET", "/admin/comments/" + id + "/spam"},
		{"POST", "/admin/comments/" + id + "/spam"},
		{"POST", "/admin/comments/" + id + "/trash"},
		{"GET", "/admin/comments/" + id + "/reply"},
		{"POST", "/admin/comments/" + id + "/reply"},
		{"GET", "/admin/comments/" + id + "/edit"},
		{"POST", "/admin/comments/" + id + "/edit"},
		{"POST", "/admin/entities/" + id + "/comments"},
		{"GET", "/admin/settings/comments"},
		{"GET", "/admin/settings/comments/blocklists"},
		{"POST", "/admin/settings/comments/blocklists"},
		{"GET", "/admin/settings/comments/commenters"},
		{"POST", "/admin/settings/comments/commenters/" + id + "/vouch"},
		{"POST", "/admin/settings/comments/commenters/" + id + "/block"},
		{"POST", "/admin/settings/comments/commenters/" + id + "/signout"},
	}
	for _, rt := range routes {
		var form url.Values
		if rt.method == "POST" {
			form = url.Values{}
		}
		if rec := p.do(rt.method, rt.path, "editor-token", form, nil); rec.Code == 403 || rec.Code == 404 {
			t.Errorf("editor %s %s = %d, want it reachable", rt.method, rt.path, rec.Code)
		}
	}
}
