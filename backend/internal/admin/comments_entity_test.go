package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

// centityRig is the content rig with a comment service wired and an audit sink.
func centityRig(t *testing.T, svc *fakeComments) (*contentRig, *[]AuditEvent) {
	t.Helper()
	var events []AuditEvent
	c := newContentRigWith(t, func(d *Deps) {
		d.Comments = svc
		d.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }
	})
	return c, &events
}

func centityPath(n int) string { return "/admin/entities/" + fid(n).String() }

func TestEntityCommentsCardHiddenWhenOff(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{entity: EntityComments{Resolved: "open", Total: 4, Pending: 1}}
	c, _ := centityRig(t, svc)
	body := c.get(t, "owner-token", centityPath(11), "")
	lacks(t, body, `id="comments"`, `id="be-comments"`, `hm-commentsform`)

	// No service at all: same.
	c2 := newContentRig(t)
	lacks(t, c2.get(t, "owner-token", centityPath(11), ""), `id="comments"`, `hm-commentsform`)
}

func TestEntityCommentsCardWhenOn(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{settings: CommentSettings{Enabled: true}, entity: EntityComments{Override: "closed", Resolved: "open", Total: 4, Pending: 1}}
	c, _ := centityRig(t, svc)
	body := c.get(t, "owner-token", centityPath(11), "")
	id := fid(11).String()
	has(t, body, `id="comments"`, `name="comments_mode"`, `form="hm-commentsform"`, `Inherit (open)`, `<option value="closed" selected>`,
		`4 comments (1 pending)`, `href="/admin/comments?entity=`+id+`&amp;status=pending"`,
		`<form method="post" action="/admin/entities/`+id+`/comments" id="hm-commentsform">`)
	// The card's form follows the editor's form: forms never nest.
	i, j := strings.Index(body, `id="hm-entityform"`), strings.Index(body, `id="hm-commentsform"`)
	if end := strings.Index(body[i:], "</form>"); i < 0 || j < i+end {
		t.Errorf("comments form is inside the editor form (%d, %d)", i, j)
	}

	svc.mu.Lock()
	svc.entity = EntityComments{Resolved: "open", Total: 1}
	svc.mu.Unlock()
	body = c.get(t, "owner-token", centityPath(11), "")
	has(t, body, `1 comment (0 pending)`)
}

func TestEntityCommentsCardOnBlogEditor(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	r.a.deps.Comments = &fakeComments{settings: CommentSettings{Enabled: true}, entity: EntityComments{Resolved: "open", Total: 2}}
	body := r.get(t, "owner-token", r.path(), "")
	has(t, body, `id="be-comments"`, `2 comments (0 pending)`, `id="hm-commentsform"`)
}

func TestEntityCommentsSave(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{settings: CommentSettings{Enabled: true}, entity: EntityComments{Override: "open", Resolved: "open"}}
	c, events := centityRig(t, svc)
	for _, token := range []string{"owner-token", "editor-token"} {
		svc.calls, *events = nil, nil
		rec := c.do("POST", centityPath(11)+"/comments", token, url.Values{"comments_mode": {"closed"}}, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != centityPath(11) {
			t.Fatalf("%s: %d -> %q", token, rec.Code, rec.Header().Get("Location"))
		}
		if len(svc.calls) != 1 || svc.calls[0] != "entity-mode" || svc.lastMode != "closed" || svc.lastIDs[0] != fid(11).String() {
			t.Errorf("%s: calls %v mode %q ids %v", token, svc.calls, svc.lastMode, svc.lastIDs)
		}
		if len(*events) != 1 || (*events)[0].Action != "comments.entity-mode" || (*events)[0].Detail["old"] != "open" || (*events)[0].Detail["new"] != "closed" {
			t.Errorf("%s: audit %+v", token, *events)
		}
	}
	// Back is honoured only inside the admin entities.
	rec := c.do("POST", centityPath(11)+"/comments", "owner-token", url.Values{"comments_mode": {""}, "back": {"//evil.example"}}, nil)
	if rec.Header().Get("Location") != centityPath(11) {
		t.Errorf("open redirect: %q", rec.Header().Get("Location"))
	}
}

func TestEntityCommentsBadMode(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{settings: CommentSettings{Enabled: true}}
	c, events := centityRig(t, svc)
	rec := c.do("POST", centityPath(11)+"/comments", "owner-token", url.Values{"comments_mode": {"bogus"}}, nil)
	if rec.Code != 422 {
		t.Errorf("bad mode = %d, want 422", rec.Code)
	}
	if len(svc.calls) != 0 || len(*events) != 0 {
		t.Errorf("reached the service: %v %v", svc.calls, *events)
	}
}
