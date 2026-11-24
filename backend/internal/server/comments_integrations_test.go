package server

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/pluginkey"
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
	o.OnSettingsChanged(ctx, comment.Settings{})
	o.OnBatch(ctx, []uuid.UUID{c.EntityID})
	if n := names(*got); len(n) != 1 || n[0] != "comment.approved" {
		t.Errorf("fired %v", n)
	}
}

func TestCommentEventUpdated(t *testing.T) {
	ctx := comment.WithBatch(context.Background()) // a batch still announces each comment
	o, got := recordingCommentObserver()
	before := testComment(comment.StatusApproved)
	dead := before
	dead.Status = comment.StatusDead
	o.OnUpdated(ctx, before, dead)
	o.OnUpdated(ctx, before, testComment(comment.StatusDraft)) // a draft never fires
	if len(*got) != 1 || (*got)[0].name != "comment.updated" || (*got)[0].ev.Status != "dead" || (*got)[0].ev.ID != dead.ID.String() {
		t.Errorf("fired %+v", *got)
	}
}

// ctxCommenterObserver records the commenter each event's context carries.
func ctxCommenterObserver() (*commentEventObserver, *[]uuid.UUID) {
	var got []uuid.UUID
	o := &commentEventObserver{fire: func(ctx context.Context, _ string, _ sdk.CommentEvent) {
		id, _ := pluginkey.CommenterFrom(ctx)
		got = append(got, id)
	}}
	return o, &got
}

func TestCommentEventUpdatedCarriesTheAuthorNotTheCaller(t *testing.T) {
	moderator := uuid.New()
	ctx := pluginkey.WithCommenter(context.Background(), moderator)
	o, got := ctxCommenterObserver()

	c := testComment(comment.StatusApproved)
	c.AuthorUserID = nil
	dead := c
	dead.Status = comment.StatusDead
	o.OnUpdated(ctx, c, dead)
	o.OnUpdated(context.Background(), c, dead) // the author's own edit: no commenter on the context
	if len(*got) != 2 || (*got)[0] != *c.CommenterID || (*got)[1] != *c.CommenterID {
		t.Errorf("commenters = %v, want the author %v twice", *got, *c.CommenterID)
	}

	staff := testComment(comment.StatusApproved)
	staff.CommenterID = nil // staff and anonymised rows have none
	*got = nil
	o.OnUpdated(ctx, staff, staff)
	o.OnDeleted(ctx, staff)
	if len(*got) != 2 || (*got)[0] != uuid.Nil || (*got)[1] != uuid.Nil {
		t.Errorf("commenters = %v, want none (not the caller's %v)", *got, moderator)
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
	b, err := jsonx.MarshalPinned(ev)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]any
	if err := jsonx.Unmarshal(b, &keys); err != nil {
		t.Fatal(err)
	}
	gotKeys := slices.Sorted(maps.Keys(keys))
	if want := []string{"entity_id", "id", "parent_id", "status"}; !slices.Equal(gotKeys, want) {
		t.Errorf("deleted payload keys %v, want %v (docs: ids and status only)", gotKeys, want)
	}
}

func TestCommentEventNeverLeaksPrivateFields(t *testing.T) {
	c := testComment(comment.StatusPending)
	b, err := jsonx.Marshal(commentEvent(c))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"SECRET-RAW", "IPHASH-X", "NETHASH-X", "UA-X", "BODYHASH-X", "REASON-X", "VERDICT-X", c.CommenterID.String(), c.AuthorUserID.String(), "commenter_key"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("payload leaks %q: %s", bad, b)
		}
	}
	if ev := commentEvent(c); !ev.Staff || ev.ParentID != c.ParentID.String() || ev.BodyHTML != "<p>hi</p>" {
		t.Errorf("event %+v", ev)
	}
}

func TestPluginCommentReaderWithoutComments(t *testing.T) {
	r := pluginCommentReader{s: &Server{}}
	id := uuid.New()
	th, err := r.Thread(context.Background(), id, 0, pluginhost.ThreadOpts{})
	if err != nil || th.Items == nil || len(th.Items) != 0 || th.Page != 1 {
		t.Fatalf("thread %+v err %v", th, err)
	}
	b, _ := jsonx.Marshal(th)
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
	commenter := uuid.New()
	page := comment.ThreadPage{Page: 2, HasMore: true, Comments: []comment.ThreadComment{
		{ID: uuid.New(), ParentID: &parent, Depth: 1, AuthorName: "Bo", IsSiteAuthor: true, BodyHTML: "<p>x</p>", CreatedAt: now, EditedAt: &edited},
		{ID: uuid.New(), AuthorName: "Cy", BodyHTML: "<p>y</p>", CreatedAt: now, CommenterID: &commenter},
	}}
	th := pluginThread(page, false, nil)
	if th.Page != 2 || !th.HasMore || len(th.Items) != 2 {
		t.Fatalf("thread %+v", th)
	}
	it := th.Items[0]
	if it.ParentID != parent.String() || it.Depth != 1 || !it.Staff || it.AuthorName != "Bo" || it.BodyHTML != "<p>x</p>" {
		t.Errorf("item %+v", it)
	}
	if th.Items[1].CommenterKey != "" {
		t.Error("a thread read without keys carries a key")
	}
	if empty := pluginThread(comment.ThreadPage{Page: 1}, false, nil); empty.Items == nil {
		t.Error("empty thread must have non-nil items")
	}
	if it.Status != "approved" {
		t.Errorf("status %q, want approved", it.Status)
	}

	// Two plugins get different keys for the same commenter; the staff row has none.
	keys := pluginkey.New("test-secret")
	a := pluginThread(page, false, func(id uuid.UUID) string { return keys.Commenter("plugin-a", id) })
	b := pluginThread(page, false, func(id uuid.UUID) string { return keys.Commenter("plugin-b", id) })
	if a.Items[0].CommenterKey != "" || b.Items[0].CommenterKey != "" {
		t.Error("a staff item carries a commenter key")
	}
	if a.Items[1].CommenterKey == "" || a.Items[1].CommenterKey == b.Items[1].CommenterKey {
		t.Errorf("keys: plugin-a %q, plugin-b %q", a.Items[1].CommenterKey, b.Items[1].CommenterKey)
	}
	if strings.Contains(a.Items[1].CommenterKey, commenter.String()) {
		t.Error("the key contains the commenter id")
	}
}

func TestPluginThreadDeadMasking(t *testing.T) {
	commenterID := uuid.New()
	tp := comment.ThreadPage{Comments: []comment.ThreadComment{
		{ID: uuid.New(), AuthorName: "Bo", IsSiteAuthor: true, BodyHTML: "<p>x</p>", Status: comment.StatusDead, Rank: 2.5},
		{ID: uuid.New(), AuthorName: "Al", BodyHTML: "<p>y</p>", Status: comment.StatusApproved, Rank: 1},
	}}
	masked := pluginThread(tp, false, nil).Items
	if d := masked[0]; d.Status != "dead" || d.AuthorName != "" || d.BodyHTML != "" || d.Staff || d.Rank != 2.5 {
		t.Errorf("dead without reveal: %+v", d)
	}
	if a := masked[1]; a.Status != "approved" || a.BodyHTML != "<p>y</p>" || a.Rank != 1 {
		t.Errorf("approved: %+v", a)
	}
	keyed := comment.ThreadPage{Comments: []comment.ThreadComment{{ID: uuid.New(), Status: comment.StatusDead, CommenterID: &commenterID}}}
	key := func(uuid.UUID) string { return "k" }
	if d := pluginThread(keyed, false, key).Items[0]; d.CommenterKey != "" {
		t.Errorf("dead without reveal carries a key: %+v", d)
	}
	if d := pluginThread(keyed, true, key).Items[0]; d.CommenterKey != "k" {
		t.Errorf("dead with reveal lost its key: %+v", d)
	}
	if d := pluginThread(tp, true, nil).Items[0]; d.Status != "dead" || d.AuthorName != "Bo" || d.BodyHTML != "<p>x</p>" || !d.Staff {
		t.Errorf("dead with reveal: %+v", d)
	}
}

func TestPluginCommentReaderRevealDead(t *testing.T) {
	own := comment.EntityConfig{OwnerPlugin: "example", SchemaSlug: "story"}
	opts := pluginhost.ThreadOpts{Plugin: "example", Schema: "story", IncludeDead: true}
	if !revealDead(own, opts) {
		t.Error("owner on its own schema must reveal")
	}
	for name, c := range map[string]struct {
		info comment.EntityConfig
		opts pluginhost.ThreadOpts
	}{
		"not asked":      {own, pluginhost.ThreadOpts{Plugin: "example", Schema: "story"}},
		"other schema":   {comment.EntityConfig{OwnerPlugin: "example", SchemaSlug: "page"}, opts},
		"other plugin":   {comment.EntityConfig{OwnerPlugin: "other", SchemaSlug: "story"}, opts},
		"owner type":     {comment.EntityConfig{SchemaSlug: "story"}, opts},
		"empty identity": {comment.EntityConfig{}, pluginhost.ThreadOpts{IncludeDead: true}},
	} {
		if revealDead(c.info, c.opts) {
			t.Errorf("%s: must not reveal", name)
		}
	}
}
