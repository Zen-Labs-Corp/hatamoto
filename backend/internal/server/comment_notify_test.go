package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/user"
)

type fakeNotifyStore struct {
	mode         comment.NotifyMode
	batches      []comment.ModerationBatch
	replyBatches []comment.ReplyBatch
	replyErr     error
	windows      []time.Duration
	gaps         []time.Duration
	claims       int // ClaimModeration + ClaimReplyBatches calls
	cleared      int
}

func (f *fakeNotifyStore) Settings(context.Context) (comment.Settings, error) {
	return comment.Settings{NotifyMode: f.mode}, nil
}

func (f *fakeNotifyStore) ClaimModeration(_ context.Context, gap time.Duration) ([]comment.ModerationBatch, error) {
	f.claims++
	f.gaps = append(f.gaps, gap)
	return f.batches, nil
}

func (f *fakeNotifyStore) ClearModeration(context.Context) error { f.cleared++; return nil }

func (f *fakeNotifyStore) ClaimReplyBatches(_ context.Context, _ int, window time.Duration) ([]comment.ReplyBatch, error) {
	f.claims++
	f.windows = append(f.windows, window)
	return f.replyBatches, f.replyErr
}

func (f *fakeNotifyStore) PruneReplyQueue(context.Context) (int64, error) { return 0, nil }

type sentModeration struct {
	to string
	d  email.CommentModerationData
}

type sentReply struct {
	to string
	d  email.CommentReplyData
}

type sentReplyDigest struct {
	to string
	d  email.CommentReplyDigestData
}

type fakeNotifyMailer struct {
	configured bool
	mods       []sentModeration
	replies    []sentReply
	digests    []sentReplyDigest
	failFirst  bool
}

func (f *fakeNotifyMailer) IsConfigured() bool { return f.configured }

func (f *fakeNotifyMailer) SendCommentModeration(_ context.Context, to string, d email.CommentModerationData) error {
	f.mods = append(f.mods, sentModeration{to, d})
	if f.failFirst && len(f.mods) == 1 {
		return errors.New("smtp down")
	}
	return nil
}

func (f *fakeNotifyMailer) SendCommentReplyDigest(_ context.Context, to string, d email.CommentReplyDigestData) error {
	f.digests = append(f.digests, sentReplyDigest{to, d})
	return nil
}

func (f *fakeNotifyMailer) SendCommentReply(_ context.Context, to string, d email.CommentReplyData) error {
	f.replies = append(f.replies, sentReply{to, d})
	if f.failFirst && len(f.replies) == 1 {
		return errors.New("smtp down")
	}
	return nil
}

type notifyFixture struct {
	n     *commentNotifier
	store *fakeNotifyStore
	mail  *fakeNotifyMailer
	mod   *user.User
	page  uuid.UUID
}

func newNotifyFixture(base string) *notifyFixture {
	f := &notifyFixture{
		store: &fakeNotifyStore{mode: comment.NotifyPerComment},
		mail:  &fakeNotifyMailer{configured: true},
		mod:   &user.User{ID: uuid.New(), Email: "mod@example.test", Role: user.RoleOwner},
		page:  uuid.New(),
	}
	f.n = &commentNotifier{
		store: f.store, mail: f.mail,
		moderators: func(context.Context) ([]*user.User, error) { return []*user.User{f.mod}, nil },
		site:       func(context.Context) (string, string, string) { return "Site", base, "/admin" },
		entity: func(_ context.Context, id uuid.UUID) (string, string, bool) {
			return "Page", "/p", id == f.page
		},
		unsubKey: comment.UnsubscribeKey("notify-test-secret"),
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return f
}

func (f *notifyFixture) held(n int) comment.ModerationBatch {
	b := comment.ModerationBatch{UserID: f.mod.ID}
	for range n {
		b.Comments = append(b.Comments, comment.Comment{
			ID: uuid.New(), EntityID: f.page, AuthorName: "Ada", SpamReasons: []string{"new commenter"},
		})
	}
	return b
}

func TestCommentNotifyUnconfiguredOrNoBaseKeepsQueues(t *testing.T) {
	f := newNotifyFixture("https://site.test")
	f.mail.configured = false
	if err := f.n.run(context.Background()); err != nil || f.store.claims != 0 {
		t.Fatalf("unconfigured: err %v, claims %d", err, f.store.claims)
	}
	f = newNotifyFixture("")
	if err := f.n.run(context.Background()); err != nil || f.store.claims != 0 {
		t.Fatalf("no base: err %v, claims %d", err, f.store.claims)
	}
}

func TestCommentNotifyModeOffClearsAndSendsNothing(t *testing.T) {
	f := newNotifyFixture("https://site.test")
	f.store.mode = comment.NotifyOff
	f.store.batches = []comment.ModerationBatch{f.held(1)}
	if err := f.n.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.store.cleared != 1 || len(f.mail.mods) != 0 {
		t.Errorf("cleared %d, sent %d", f.store.cleared, len(f.mail.mods))
	}
}

func TestCommentNotifyGapFollowsMode(t *testing.T) {
	for mode, want := range map[comment.NotifyMode]time.Duration{
		comment.NotifyPerComment: 10 * time.Minute, comment.NotifyDigest: time.Hour,
	} {
		f := newNotifyFixture("https://site.test")
		f.store.mode = mode
		if err := f.n.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(f.store.gaps) != 1 || f.store.gaps[0] != want {
			t.Errorf("%s: gaps %v, want %v", mode, f.store.gaps, want)
		}
	}
}

func TestCommentNotifyModerationItems(t *testing.T) {
	f := newNotifyFixture("https://site.test/")
	f.store.batches = []comment.ModerationBatch{f.held(1)}
	if err := f.n.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.mail.mods) != 1 || f.mail.mods[0].to != f.mod.Email || len(f.mail.mods[0].d.Items) != 1 {
		t.Fatalf("sent = %+v", f.mail.mods)
	}
	d := f.mail.mods[0].d
	if d.Items[0].Held != "first comment" || d.Items[0].EntityTitle != "Page" || d.ModerateURL != "https://site.test/admin/comments?status=pending" {
		t.Errorf("data = %+v", d)
	}
	kind, id, ok := comment.VerifyUnsubscribe(f.n.unsubKey, d.UnsubscribeURL[len("https://site.test"+commentUnsubscribePrefix):])
	if !ok || kind != comment.UnsubscribeModerator || id != f.mod.ID {
		t.Errorf("unsubscribe URL %q verifies to %c %s %v", d.UnsubscribeURL, kind, id, ok)
	}

	f = newNotifyFixture("https://site.test")
	f.store.batches = []comment.ModerationBatch{f.held(25)}
	if err := f.n.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.mail.mods[0].d; len(got.Items) != 20 || got.More != 5 {
		t.Errorf("items %d, more %d; want 20, 5", len(got.Items), got.More)
	}
}

func TestCommentNotifySkipsUnknownModerator(t *testing.T) {
	f := newNotifyFixture("https://site.test")
	b := f.held(1)
	b.UserID = uuid.New()
	f.store.batches = []comment.ModerationBatch{b}
	if err := f.n.run(context.Background()); err != nil || len(f.mail.mods) != 0 {
		t.Fatalf("err %v, sent %d", err, len(f.mail.mods))
	}
}

func (f *notifyFixture) reply(entity uuid.UUID, name string) comment.Comment {
	return comment.Comment{ID: uuid.New(), EntityID: entity, AuthorName: name}
}

func unsubVerifies(t *testing.T, f *notifyFixture, url string, want uuid.UUID) {
	t.Helper()
	kind, id, ok := comment.VerifyUnsubscribe(f.n.unsubKey, url[len("https://site.test"+commentUnsubscribePrefix):])
	if !ok || kind != comment.UnsubscribeReplies || id != want {
		t.Errorf("unsubscribe URL verifies to %c %s %v", kind, id, ok)
	}
}

func TestCommentNotifyReplyMail(t *testing.T) {
	f := newNotifyFixture("https://site.test")
	cid := uuid.New()
	rep := f.reply(f.page, "Bob")
	f.store.replyBatches = []comment.ReplyBatch{
		{CommenterID: cid, Email: "ada@example.test", Replies: []comment.Comment{rep}},
		{CommenterID: uuid.New(), Email: "gone@example.test", Replies: []comment.Comment{f.reply(uuid.New(), "")}},
	}
	if err := f.n.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.mail.replies) != 1 || len(f.mail.digests) != 0 {
		t.Fatalf("sent %d replies, %d digests, want 1 reply (unknown entity skipped)", len(f.mail.replies), len(f.mail.digests))
	}
	r := f.mail.replies[0]
	if r.to != "ada@example.test" || r.d.URL != "https://site.test/p#comment-"+rep.ID.String() || r.d.ReplierName != "Bob" {
		t.Errorf("reply = %+v", r)
	}
	unsubVerifies(t, f, r.d.UnsubscribeURL, cid)
}

func TestCommentNotifyReplyDigest(t *testing.T) {
	f := newNotifyFixture("https://site.test")
	cid := uuid.New()
	a, b, c := f.reply(f.page, "Bob"), f.reply(uuid.New(), "Gone"), f.reply(f.page, "Cy")
	f.store.replyBatches = []comment.ReplyBatch{{CommenterID: cid, Email: "ada@example.test", Replies: []comment.Comment{a, b, c}}}
	if err := f.n.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.mail.digests) != 1 || len(f.mail.replies) != 0 {
		t.Fatalf("sent %d digests, %d replies, want 1 digest", len(f.mail.digests), len(f.mail.replies))
	}
	d := f.mail.digests[0]
	if d.to != "ada@example.test" || d.d.SiteName != "Site" || d.d.More != 0 || len(d.d.Items) != 2 {
		t.Fatalf("digest = %+v", d)
	}
	want := []email.CommentReplyItem{
		{ReplierName: "Bob", EntityTitle: "Page", URL: "https://site.test/p#comment-" + a.ID.String()},
		{ReplierName: "Cy", EntityTitle: "Page", URL: "https://site.test/p#comment-" + c.ID.String()},
	}
	for i := range want {
		if d.d.Items[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, d.d.Items[i], want[i])
		}
	}
	unsubVerifies(t, f, d.d.UnsubscribeURL, cid)
}

func TestCommentNotifyReplyDigestOneResolvableIsSingle(t *testing.T) {
	f := newNotifyFixture("https://site.test")
	f.store.replyBatches = []comment.ReplyBatch{{CommenterID: uuid.New(), Email: "ada@example.test",
		Replies: []comment.Comment{f.reply(uuid.New(), "Gone"), f.reply(f.page, "Bob")}}}
	if err := f.n.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.mail.replies) != 1 || len(f.mail.digests) != 0 {
		t.Fatalf("sent %d replies, %d digests, want a single reply mail", len(f.mail.replies), len(f.mail.digests))
	}
}

func TestCommentNotifyReplyDigestCapsItems(t *testing.T) {
	f := newNotifyFixture("https://site.test")
	b := comment.ReplyBatch{CommenterID: uuid.New(), Email: "ada@example.test"}
	for range 25 {
		b.Replies = append(b.Replies, f.reply(f.page, "Bob"))
	}
	f.store.replyBatches = []comment.ReplyBatch{b}
	if err := f.n.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.mail.digests) != 1 {
		t.Fatalf("sent %d digests, want 1", len(f.mail.digests))
	}
	if d := f.mail.digests[0].d; len(d.Items) != 20 || d.More != 5 {
		t.Errorf("items %d, more %d; want 20, 5", len(d.Items), d.More)
	}
}

func TestCommentNotifyReplyWindow(t *testing.T) {
	f := newNotifyFixture("https://site.test")
	if err := f.n.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.store.windows) != 1 || f.store.windows[0] != 15*time.Minute {
		t.Errorf("claim windows = %v, want [15m]", f.store.windows)
	}
}

func TestCommentNotifyClaimErrorStillMailsClaimedBatches(t *testing.T) {
	f := newNotifyFixture("https://site.test")
	f.store.replyBatches = []comment.ReplyBatch{{CommenterID: uuid.New(), Email: "ada@example.test",
		Replies: []comment.Comment{f.reply(f.page, "Bob")}}}
	f.store.replyErr = errors.New("db down")
	if err := f.n.run(context.Background()); err == nil {
		t.Fatal("run = nil, want the claim error")
	}
	if len(f.mail.replies) != 1 {
		t.Errorf("sent %d, want the claimed batch mailed", len(f.mail.replies))
	}
}

func TestCommentNotifySendErrorDoesNotStopTheRun(t *testing.T) {
	f := newNotifyFixture("https://site.test")
	f.mail.failFirst = true
	f.store.replyBatches = []comment.ReplyBatch{
		{CommenterID: uuid.New(), Email: "a@example.test", Replies: []comment.Comment{f.reply(f.page, "")}},
		{CommenterID: uuid.New(), Email: "b@example.test", Replies: []comment.Comment{f.reply(f.page, "")}},
	}
	if err := f.n.run(context.Background()); err != nil {
		t.Fatalf("run = %v", err)
	}
	if len(f.mail.replies) != 2 {
		t.Errorf("sent %d, want 2 attempts", len(f.mail.replies))
	}
}
