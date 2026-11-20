package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	hmauth "hatamoto.dev/core/internal/auth"
)

// fakeComments records what the tools pass and returns canned results.
type fakeComments struct {
	rows []CommentRow
	err  error

	filter CommentFilter
	id     uuid.UUID
	actor  *uuid.UUID
	body   string
	calls  []string
}

func (f *fakeComments) List(_ context.Context, fl CommentFilter) ([]CommentRow, bool, error) {
	f.calls = append(f.calls, "list")
	f.filter = fl
	return f.rows, true, f.err
}

func (f *fakeComments) Approve(_ context.Context, id uuid.UUID, actor *uuid.UUID) (CommentRow, error) {
	f.calls = append(f.calls, "approve")
	f.id, f.actor = id, actor
	return CommentRow{ID: id.String(), Status: "approved"}, f.err
}

func (f *fakeComments) Spam(_ context.Context, id uuid.UUID, actor *uuid.UUID) (CommentRow, error) {
	f.calls = append(f.calls, "spam")
	f.id, f.actor = id, actor
	return CommentRow{ID: id.String(), Status: "spam"}, f.err
}

func (f *fakeComments) Reply(_ context.Context, parent, actor uuid.UUID, body string) (CommentRow, error) {
	f.calls = append(f.calls, "reply")
	f.id, f.actor, f.body = parent, &actor, body
	return CommentRow{ID: uuid.NewString(), ParentID: parent.String(), Status: "approved"}, f.err
}

func callComments(t *testing.T, keys fakeKeys, d Deps, tool string, args map[string]any) (string, bool) {
	t.Helper()
	d.Keys = keys
	d.Audit = nopAudit{}
	ts := httptest.NewServer(New(d, "test"))
	t.Cleanup(ts.Close)
	c := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	sess, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: ts.URL, HTTPClient: &http.Client{Transport: bearerRT{"good"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	text := ""
	if len(res.Content) > 0 {
		text = res.Content[0].(*mcp.TextContent).Text
	}
	return text, res.IsError
}

func ck(role string, scopes ...hmauth.APIKeyScope) fakeKeys {
	return fakeKeys{scopes: scopes, role: role}
}

func TestCommentsScopes(t *testing.T) {
	cid := uuid.NewString()
	calls := map[string]map[string]any{
		"comments_list":    {},
		"comments_approve": {"id": cid},
		"comments_spam":    {"id": cid},
		"comments_reply":   {"id": cid, "body": "thanks"},
	}
	cases := []struct {
		name    string
		keys    fakeKeys
		allowed map[string]bool
	}{
		{"read", ck("owner", hmauth.ScopeReadComments), map[string]bool{"comments_list": true}},
		{"write", ck("owner", hmauth.ScopeWriteComments), map[string]bool{"comments_list": true, "comments_approve": true, "comments_spam": true, "comments_reply": true}},
		{"editor", ck("editor", hmauth.ScopeWriteComments), map[string]bool{"comments_list": true, "comments_approve": true, "comments_spam": true, "comments_reply": true}},
		{"all", ck("owner", hmauth.ScopeAll), map[string]bool{"comments_list": true, "comments_approve": true, "comments_spam": true, "comments_reply": true}},
		{"entities only", ck("owner", hmauth.ScopeWriteEntities), nil},
	}
	for _, tc := range cases {
		for tool, args := range calls {
			f := &fakeComments{}
			text, isErr := callComments(t, tc.keys, Deps{Comments: f}, tool, args)
			if tc.allowed[tool] {
				if isErr {
					t.Errorf("%s/%s refused: %s", tc.name, tool, text)
				}
				continue
			}
			if !isErr || !strings.Contains(text, "forbidden") || len(f.calls) != 0 {
				t.Errorf("%s/%s = %v %q (calls %v), want forbidden", tc.name, tool, isErr, text, f.calls)
			}
		}
	}
}

func TestCommentsListDefaultsAndLimits(t *testing.T) {
	f := &fakeComments{}
	text, isErr := callComments(t, ck("owner", hmauth.ScopeReadComments), Deps{Comments: f}, "comments_list", map[string]any{})
	if isErr || f.filter.Status != "pending" || f.filter.Limit != 20 || f.filter.Page != 1 {
		t.Fatalf("defaults: %v %q %+v", isErr, text, f.filter)
	}
	if !strings.Contains(text, `"comments":[]`) || !strings.Contains(text, `"has_more":true`) {
		t.Errorf("result = %s", text)
	}
	eid := uuid.New()
	callComments(t, ck("owner", hmauth.ScopeReadComments), Deps{Comments: f}, "comments_list",
		map[string]any{"limit": 500, "status": "spam", "entity_id": eid.String(), "page": 3})
	if f.filter.Limit != 100 || f.filter.Status != "spam" || f.filter.Page != 3 || f.filter.EntityID == nil || *f.filter.EntityID != eid {
		t.Errorf("filter = %+v", f.filter)
	}
	f.calls = nil
	text, isErr = callComments(t, ck("owner", hmauth.ScopeReadComments), Deps{Comments: f}, "comments_list", map[string]any{"status": "draft"})
	if !isErr || !strings.Contains(text, "status must be") || len(f.calls) != 0 {
		t.Errorf("draft = %v %q", isErr, text)
	}
}

func TestCommentsActor(t *testing.T) {
	f := &fakeComments{}
	cid := uuid.New()
	for _, tool := range []string{"comments_approve", "comments_spam", "comments_reply"} {
		f.actor = nil
		args := map[string]any{"id": cid.String()}
		if tool == "comments_reply" {
			args["body"] = "ok"
		}
		if _, isErr := callComments(t, ck("owner", hmauth.ScopeWriteComments), Deps{Comments: f}, tool, args); isErr {
			t.Fatalf("%s failed", tool)
		}
		if f.actor == nil || *f.actor != fakeOwner || f.id != cid {
			t.Errorf("%s: actor %v id %v", tool, f.actor, f.id)
		}
	}
	if f.body != "ok" {
		t.Errorf("reply body = %q", f.body)
	}
}

func TestCommentsUnavailable(t *testing.T) {
	text, isErr := callComments(t, ck("owner", hmauth.ScopeAll), Deps{}, "comments_list", map[string]any{})
	if !isErr || !strings.Contains(text, "not available") {
		t.Fatalf("nil Comments = %v %q", isErr, text)
	}
}

func TestCommentsErrorsSurface(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{ErrCommentNotFound, "comment not found"},
		{ErrCommentState, "cannot change to that status"},
		{ErrCommentInvalid, "empty or too long"},
		{ErrCommentsUnavailable, "comments are not available"},
		{errors.New("pq: secret detail"), "internal error"},
	}
	for _, tc := range cases {
		f := &fakeComments{err: tc.err}
		text, isErr := callComments(t, ck("owner", hmauth.ScopeAll), Deps{Comments: f}, "comments_approve", map[string]any{"id": uuid.NewString()})
		if !isErr || !strings.Contains(text, tc.want) || strings.Contains(text, "secret") {
			t.Errorf("%v -> %v %q, want %q", tc.err, isErr, text, tc.want)
		}
	}
}

func TestCommentsListHasNoPrivateFields(t *testing.T) {
	f := &fakeComments{rows: []CommentRow{{
		ID: uuid.NewString(), EntityID: uuid.NewString(), EntityTitle: "Post", AuthorName: "Ann", BodyHTML: "<p>hi</p>",
		Status: "pending", Trust: "new", SpamScore: 3, HeldReasons: []string{"links"}, HasLinks: true, CreatedAt: time.Now(),
	}}}
	text, isErr := callComments(t, ck("owner", hmauth.ScopeReadComments), Deps{Comments: f}, "comments_list", map[string]any{})
	if isErr || !strings.Contains(text, "Ann") {
		t.Fatalf("list = %v %q", isErr, text)
	}
	for _, bad := range []string{"email", "hash", `"ip`, "user_agent", "commenter", "body_raw"} {
		if strings.Contains(text, bad) {
			t.Errorf("list result contains %q: %s", bad, text)
		}
	}
}
