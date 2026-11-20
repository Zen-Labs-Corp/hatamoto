package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/sdk"
)

type firedEvent struct {
	name string
	ev   sdk.CommentEvent
}

func recordingCommentObserver() (*commentEventObserver, *[]firedEvent) {
	var got []firedEvent
	o := &commentEventObserver{fire: func(_ context.Context, name string, ev sdk.CommentEvent) {
		got = append(got, firedEvent{name, ev})
	}}
	return o, &got
}

func testComment(status comment.Status) comment.Comment {
	parent, user, commenter := uuid.New(), uuid.New(), uuid.New()
	return comment.Comment{
		ID: uuid.New(), EntityID: uuid.New(), ParentID: &parent, Status: status,
		BodyRaw: "SECRET-RAW", BodyHTML: "<p>hi</p>", AuthorName: "Ann", AuthorUserID: &user,
		CommenterID: &commenter, IPHash: "IPHASH-X", NetHash: "NETHASH-X", UserAgent: "UA-X",
		BodyHash: "BODYHASH-X", SpamReasons: []string{"REASON-X"}, SpamScore: 77, SpamVerdict: "VERDICT-X",
		Locale: "en", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func names(got []firedEvent) []string {
	out := []string{}
	for _, f := range got {
		out = append(out, f.name)
	}
	return out
}

func TestCommentEventCreated(t *testing.T) {
	ctx := context.Background()
	for _, st := range []comment.Status{comment.StatusPending, comment.StatusApproved} {
		o, got := recordingCommentObserver()
		o.OnCreated(ctx, testComment(st))
		o.OnConfirmed(ctx, testComment(st))
		if n := names(*got); len(n) != 2 || n[0] != "comment.created" || n[1] != "comment.created" {
			t.Errorf("%s: fired %v", st, n)
		}
	}
	o, got := recordingCommentObserver()
	o.OnCreated(ctx, testComment(comment.StatusDraft))
	o.OnConfirmed(ctx, testComment(comment.StatusDraft))
	if len(*got) != 0 {
		t.Errorf("draft fired %v", names(*got))
	}
}

func TestCommentEventSpamStoreIsQuiet(t *testing.T) {
	ctx := context.Background()
	o, got := recordingCommentObserver()
	c := testComment(comment.StatusSpam)
	o.OnCreated(ctx, c)
	o.OnSpam(ctx, c)
	if len(*got) != 0 {
		t.Fatalf("bot spam fired %v", names(*got))
	}
	n := 0
	o.quietSpam.Range(func(_, _ any) bool { n++; return true })
	if n != 0 {
		t.Errorf("quietSpam leaked %d entries", n)
	}
	o.OnSpam(ctx, c) // a later moderator action
	if n := names(*got); len(n) != 1 || n[0] != "comment.spam" {
		t.Errorf("moderator spam fired %v", n)
	}
}

func TestCommentEventApprovedAndSilentCallbacks(t *testing.T) {
	ctx := context.Background()
	o, got := recordingCommentObserver()
	c := testComment(comment.StatusApproved)
	o.OnApproved(ctx, c)
	o.OnUpdated(ctx, c, c)
	o.OnSettingsChanged(ctx, comment.Settings{})
	if n := names(*got); len(n) != 1 || n[0] != "comment.approved" {
		t.Errorf("fired %v", n)
	}
}

func TestCommentEventDeletedCarriesIDsOnly(t *testing.T) {
	ctx := context.Background()
	o, got := recordingCommentObserver()
	c := testComment(comment.StatusApproved)
	o.OnDeleted(ctx, c)
	o.OnDeleted(ctx, testComment(comment.StatusDraft))
	if len(*got) != 1 || (*got)[0].name != "comment.deleted" {
		t.Fatalf("fired %v", names(*got))
	}
	ev := (*got)[0].ev
	if ev.BodyHTML != "" || ev.AuthorName != "" || ev.Staff || ev.ID != c.ID.String() || ev.Status != "approved" {
		t.Errorf("deleted payload %+v", ev)
	}
}

func TestCommentEventNeverLeaksPrivateFields(t *testing.T) {
	c := testComment(comment.StatusPending)
	b, err := json.Marshal(commentEvent(c))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"SECRET-RAW", "IPHASH-X", "NETHASH-X", "UA-X", "BODYHASH-X", "REASON-X", "VERDICT-X", c.CommenterID.String(), c.AuthorUserID.String()} {
		if strings.Contains(string(b), bad) {
			t.Errorf("payload leaks %q: %s", bad, b)
		}
	}
	if ev := commentEvent(c); !ev.Staff || ev.ParentID != c.ParentID.String() || ev.BodyHTML != "<p>hi</p>" {
		t.Errorf("event %+v", ev)
	}
}

func TestPluginCommentReaderWithoutComments(t *testing.T) {
	r := pluginCommentReader{&Server{}}
	id := uuid.New()
	th, err := r.Thread(context.Background(), id, 0)
	if err != nil || th.Items == nil || len(th.Items) != 0 || th.Page != 1 {
		t.Fatalf("thread %+v err %v", th, err)
	}
	b, _ := json.Marshal(th)
	if !strings.Contains(string(b), `"items":[]`) {
		t.Errorf("items not an empty array: %s", b)
	}
	counts, err := r.Counts(context.Background(), []uuid.UUID{id})
	if err != nil || len(counts) != 1 || counts[id] != 0 {
		t.Errorf("counts %v err %v", counts, err)
	}
}

func TestPluginCommentReaderThreadMapping(t *testing.T) {
	parent := uuid.New()
	now := time.Now()
	edited := now
	th := pluginThread(comment.ThreadPage{Page: 2, HasMore: true, Comments: []comment.ThreadComment{
		{ID: uuid.New(), ParentID: &parent, Depth: 1, AuthorName: "Bo", IsSiteAuthor: true, BodyHTML: "<p>x</p>", CreatedAt: now, EditedAt: &edited},
	}})
	if th.Page != 2 || !th.HasMore || len(th.Items) != 1 {
		t.Fatalf("thread %+v", th)
	}
	it := th.Items[0]
	if it.ParentID != parent.String() || it.Depth != 1 || !it.Staff || it.AuthorName != "Bo" || it.BodyHTML != "<p>x</p>" {
		t.Errorf("item %+v", it)
	}
	if empty := pluginThread(comment.ThreadPage{Page: 1}); empty.Items == nil {
		t.Error("empty thread must have non-nil items")
	}
}
