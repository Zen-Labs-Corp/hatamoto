package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/mcpserver"
)

type fakeMCPStore struct {
	rows       map[string]admin.CommentRow
	changed    int // what Approve/MarkSpam report
	err        error
	teach      admin.SpamTeach
	actor      string
	replyName  string
	replyActor string
	replyBody  string
	filter     admin.CommentFilter
}

func (f *fakeMCPStore) Queue(_ context.Context, fl admin.CommentFilter) (admin.CommentPage, error) {
	f.filter = fl
	return admin.CommentPage{Rows: []admin.CommentRow{f.rows["a"]}, HasMore: true}, f.err
}
func (f *fakeMCPStore) Comment(_ context.Context, id string) (admin.CommentRow, error) {
	r, ok := f.rows[id]
	if !ok {
		return admin.CommentRow{}, admin.ErrCommentNotFound
	}
	return r, nil
}
func (f *fakeMCPStore) Approve(_ context.Context, _ []string, actor string) (int, error) {
	f.actor = actor
	return f.changed, f.err
}
func (f *fakeMCPStore) MarkSpam(_ context.Context, _ []string, t admin.SpamTeach, actor string) (int, error) {
	f.teach, f.actor = t, actor
	return f.changed, f.err
}
func (f *fakeMCPStore) Reply(_ context.Context, _, body, actor, name string) (string, error) {
	f.replyBody, f.replyActor, f.replyName = body, actor, name
	return "new", f.err
}

func TestMCPCommentErrMapping(t *testing.T) {
	other := errors.New("boom")
	for _, tc := range []struct{ in, want error }{
		{nil, nil},
		{admin.ErrCommentNotFound, mcpserver.ErrCommentNotFound},
		{fmt.Errorf("x: %w", admin.ErrCommentEmpty), mcpserver.ErrCommentInvalid},
		{admin.ErrCommentTooLong, mcpserver.ErrCommentInvalid},
		{comment.ErrBadTransition, mcpserver.ErrCommentState},
		{other, other},
	} {
		if got := mcpCommentErr(tc.in); got != tc.want && !errors.Is(got, tc.want) {
			t.Errorf("mcpCommentErr(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestMCPCommentApproveIdempotentAndDraft(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()
	f := &fakeMCPStore{rows: map[string]admin.CommentRow{id.String(): {ID: id.String(), Status: "approved"}}}
	m := mcpComments{store: f}
	row, err := m.Approve(ctx, id, nil)
	if err != nil || row.Status != "approved" {
		t.Fatalf("already approved: %+v %v", row, err)
	}
	f.rows[id.String()] = admin.CommentRow{ID: id.String(), Status: "draft"}
	if _, err := m.Approve(ctx, id, nil); !errors.Is(err, mcpserver.ErrCommentState) {
		t.Errorf("draft: %v", err)
	}
	if _, err := m.Approve(ctx, uuid.New(), nil); !errors.Is(err, mcpserver.ErrCommentNotFound) {
		t.Errorf("unknown: %v", err)
	}
	f.rows[id.String()] = admin.CommentRow{ID: id.String(), Status: "approved"}
	f.changed = 1
	if _, err := m.Approve(ctx, id, nil); err != nil {
		t.Errorf("changed: %v", err)
	}
}

func TestMCPCommentSpamPassesActorAndNoTeach(t *testing.T) {
	id, actor := uuid.New(), uuid.New()
	f := &fakeMCPStore{changed: 1, rows: map[string]admin.CommentRow{id.String(): {ID: id.String(), Status: "spam"}}}
	row, err := mcpComments{store: f}.Spam(context.Background(), id, &actor)
	if err != nil || row.Status != "spam" {
		t.Fatalf("%+v %v", row, err)
	}
	if f.actor != actor.String() || f.teach != (admin.SpamTeach{}) {
		t.Errorf("actor %q teach %+v", f.actor, f.teach)
	}
}

func TestMCPCommentReplyPassesActorAndName(t *testing.T) {
	actor := uuid.New()
	f := &fakeMCPStore{rows: map[string]admin.CommentRow{"new": {ID: "new", Status: "approved", AuthorUserID: actor.String()}}}
	m := mcpComments{store: f, name: func(context.Context, uuid.UUID) string { return "Site" }}
	row, err := m.Reply(context.Background(), uuid.New(), actor, "thanks")
	if err != nil || row.ID != "new" || !row.Staff {
		t.Fatalf("%+v %v", row, err)
	}
	if f.replyActor != actor.String() || f.replyName != "Site" || f.replyBody != "thanks" {
		t.Errorf("reply got %q %q %q", f.replyActor, f.replyName, f.replyBody)
	}
	f.err = admin.ErrCommentEmpty
	if _, err := m.Reply(context.Background(), uuid.New(), actor, ""); !errors.Is(err, mcpserver.ErrCommentInvalid) {
		t.Errorf("empty: %v", err)
	}
}

func TestMCPCommentListFilterAndRows(t *testing.T) {
	ent := uuid.New()
	f := &fakeMCPStore{rows: map[string]admin.CommentRow{"a": {ID: "a", BodyRaw: "RAWBODY", CommenterID: "COMMENTER-1", AuthorUserID: "USER-1", Score: 5}}}
	rows, more, err := mcpComments{store: f}.List(context.Background(), mcpserver.CommentFilter{Status: "pending", EntityID: &ent, Page: 2, Limit: 10})
	if err != nil || !more || len(rows) != 1 {
		t.Fatalf("%v %v %v", rows, more, err)
	}
	if f.filter.EntityID != ent.String() || f.filter.PerPage != 10 || f.filter.Page != 2 || f.filter.Status != "pending" {
		t.Errorf("filter %+v", f.filter)
	}
	b, _ := jsonx.Marshal(rows[0])
	for _, bad := range []string{"RAWBODY", "COMMENTER-1", "USER-1", "body_raw", "commenter"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("row leaks %q: %s", bad, b)
		}
	}
}
