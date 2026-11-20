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

func cschemaPath(n int) string { return "/admin/settings/types/" + fid(n).String() }

func TestSchemaCommentsHiddenWhenOff(t *testing.T) {
	t.Parallel()

	c, _ := centityRig(t, &fakeComments{})
	lacks(t, c.get(t, "owner-token", cschemaPath(1), ""), `id="comments"`, cschemaPath(1)+`/comments"`)
	lacks(t, newContentRig(t).get(t, "owner-token", cschemaPath(1), ""), `id="comments"`)
}

func TestSchemaCommentsWhenOn(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{settings: CommentSettings{Enabled: true}, schemaMode: "hidden"}
	c, _ := centityRig(t, svc)
	body := c.get(t, "owner-token", cschemaPath(1), "")
	has(t, body, `id="comments"`, `<form method="post" action="`+cschemaPath(1)+`/comments">`, `name="mode"`, `Inherit the site setting`, `<option value="hidden" selected>`)
	// The card's form is outside the type form and outside the built-in fieldset.
	i, j := strings.Index(body, `hm-schemaform`), strings.Index(body, `action="`+cschemaPath(1)+`/comments"`)
	if end := strings.Index(body[i:], "</form>"); i < 0 || j < i+end {
		t.Errorf("comments form is inside the type form (%d, %d)", i, j)
	}
}

func TestSchemaCommentsSave(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{settings: CommentSettings{Enabled: true}, schemaMode: "open"}
	c, events := centityRig(t, svc)
	rec := c.post(cschemaPath(1)+"/comments", url.Values{"mode": {"closed"}})
	if rec.Code != 303 || rec.Header().Get("Location") != cschemaPath(1) {
		t.Fatalf("save = %d -> %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(svc.calls) != 1 || svc.calls[0] != "schema-mode" || svc.lastMode != "closed" || svc.lastIDs[0] != fid(1).String() {
		t.Errorf("calls %v mode %q", svc.calls, svc.lastMode)
	}
	if len(*events) != 1 || (*events)[0].Action != "comments.schema-mode" || (*events)[0].Detail["old"] != "open" || (*events)[0].Detail["new"] != "closed" {
		t.Errorf("audit %+v", *events)
	}
	svc.calls = nil
	if rec := c.post(cschemaPath(1)+"/comments", url.Values{"mode": {"nope"}}); rec.Code != 422 || len(svc.calls) != 0 {
		t.Errorf("bad mode = %d, calls %v", rec.Code, svc.calls)
	}
}

func TestSchemaCommentsEditor403(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{settings: CommentSettings{Enabled: true}}
	c, events := centityRig(t, svc)
	if rec := c.do("POST", cschemaPath(1)+"/comments", "editor-token", url.Values{"mode": {"closed"}}, nil); rec.Code != 403 {
		t.Errorf("editor = %d, want 403", rec.Code)
	}
	if len(svc.calls) != 0 || len(*events) != 0 {
		t.Errorf("reached the service: %v %v", svc.calls, *events)
	}
}

func TestSchemaCommentsBuiltinUnsupported(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{settings: CommentSettings{Enabled: true}}
	c, _ := centityRig(t, svc)
	body := c.get(t, "owner-token", cschemaPath(2), "") // the built-in Page type
	has(t, body, `id="comments"`, `Comment defaults for built-in types are not available yet.`)
	lacks(t, body, `action="`+cschemaPath(2)+`/comments"`)

	// A custom type gets the form; a built-in one too once the service can save it.
	has(t, c.get(t, "owner-token", cschemaPath(1), ""), `action="`+cschemaPath(1)+`/comments"`)
	svc.mu.Lock()
	svc.supports = map[string]bool{"schema-mode-builtin": true}
	svc.mu.Unlock()
	has(t, c.get(t, "owner-token", cschemaPath(2), ""), `action="`+cschemaPath(2)+`/comments"`)
}

func TestSchemaCommentsSaveKeepsKey(t *testing.T) {
	t.Parallel()

	c, _ := centityRig(t, &fakeComments{settings: CommentSettings{Enabled: true}})
	c.service.Settings = []byte(`{"url_pattern":"/services/{path}","comments":{"mode":"closed"}}`)
	rec := c.post(cschemaPath(1), schemaPost(c, func(f url.Values) { f.Set("op", "save") }))
	if rec.Code != 303 {
		t.Fatalf("save = %d\n%s", rec.Code, rec.Body.String())
	}
	if got := string(c.service.Settings); !strings.Contains(got, `"comments"`) || !strings.Contains(got, `"closed"`) {
		t.Errorf("settings lost the comments key: %s", got)
	}
}
