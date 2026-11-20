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
	"hatamoto.dev/core/internal/commentsubmit"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/user"
)

// fakeSurgeClaim applies the same rule as comment.Service.NoteSurge: win when
// there is no heartbeat or it is at least quiet old; always refresh.
type fakeSurgeClaim struct {
	now   time.Time
	last  *time.Time
	calls int
}

func (f *fakeSurgeClaim) NoteSurge(_ context.Context, quiet time.Duration) (bool, error) {
	f.calls++
	won := f.last == nil || !f.last.After(f.now.Add(-quiet))
	t := f.now
	f.last = &t
	return won, nil
}

type surgeSent struct {
	to string
	d  email.CommentSurgeData
}

type fakeSurgeMail struct {
	configured bool
	sent       []surgeSent
	failTo     string
	block      chan struct{}
}

func (f *fakeSurgeMail) IsConfigured() bool { return f.configured }

func (f *fakeSurgeMail) SendCommentSurge(_ context.Context, to string, d email.CommentSurgeData) error {
	if f.block != nil {
		<-f.block
	}
	f.sent = append(f.sent, surgeSent{to, d})
	if to == f.failTo {
		return errors.New("smtp down")
	}
	return nil
}

func surgeOwners(addrs ...string) func(context.Context) ([]*user.User, error) {
	return func(context.Context) ([]*user.User, error) {
		var out []*user.User
		for _, a := range addrs {
			out = append(out, &user.User{ID: uuid.New(), Email: a, Role: user.RoleOwner})
		}
		return out, nil
	}
}

func newSurgeNotifier(claim *fakeSurgeClaim, mail *fakeSurgeMail, base string, owners ...string) *commentSurgeNotifier {
	return &commentSurgeNotifier{
		claim: claim, mail: mail, owners: surgeOwners(owners...),
		site: func(context.Context) (string, string, string) { return "Site", base, "/admin" },
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestSurgeNotifierMailsOncePerEpisode(t *testing.T) {
	claim := &fakeSurgeClaim{now: time.Now()}
	mail := &fakeSurgeMail{configured: true}
	n := newSurgeNotifier(claim, mail, "https://site.test/", "a@example.test", "b@example.test")
	ctx := context.Background()

	if err := n.run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(mail.sent) != 2 || mail.sent[0].to != "a@example.test" || mail.sent[1].to != "b@example.test" {
		t.Fatalf("first run sent %v, want one mail per owner", mail.sent)
	}
	if got := mail.sent[0].d.ModerateURL; got != "https://site.test/admin/comments?status=pending" {
		t.Errorf("ModerateURL = %q", got)
	}

	claim.now = claim.now.Add(time.Minute)
	if err := n.run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(mail.sent) != 2 {
		t.Errorf("the same episode mailed again: %d mails", len(mail.sent))
	}

	claim.now = claim.now.Add(surgeQuiet)
	if err := n.run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(mail.sent) != 4 {
		t.Errorf("a new episode sent %d mails in total, want 4", len(mail.sent))
	}
}

// A beat at T, unreported surge comments until T+59s, the next beat at
// T+SurgeWindow: the local window never emptied, so no second mail.
func TestSurgeNotifierHeartbeatGapKeepsEpisode(t *testing.T) {
	t0 := time.Now()
	claim := &fakeSurgeClaim{now: t0}
	mail := &fakeSurgeMail{configured: true}
	n := newSurgeNotifier(claim, mail, "https://site.test", "a@example.test")
	ctx := context.Background()

	if err := n.run(ctx); err != nil {
		t.Fatal(err)
	}
	claim.now = t0.Add(commentsubmit.SurgeWindow)
	if err := n.run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("a heartbeat gap inside one surge sent %d mails, want 1", len(mail.sent))
	}
}

func TestSurgeNotifierChecksBeforeClaiming(t *testing.T) {
	for name, tc := range map[string]struct {
		configured bool
		base       string
		owners     []string
	}{
		"mail not configured": {false, "https://site.test", []string{"a@example.test"}},
		"no site address":     {true, "", []string{"a@example.test"}},
		"no owners":           {true, "https://site.test", nil},
	} {
		t.Run(name, func(t *testing.T) {
			claim := &fakeSurgeClaim{now: time.Now()}
			mail := &fakeSurgeMail{configured: tc.configured}
			if err := newSurgeNotifier(claim, mail, tc.base, tc.owners...).run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if claim.calls != 0 || len(mail.sent) != 0 {
				t.Errorf("claims = %d, mails = %d; want none", claim.calls, len(mail.sent))
			}
		})
	}
}

func TestSurgeNotifierOneFailureDoesNotStopTheRest(t *testing.T) {
	claim := &fakeSurgeClaim{now: time.Now()}
	mail := &fakeSurgeMail{configured: true, failTo: "a@example.test"}
	n := newSurgeNotifier(claim, mail, "https://site.test", "a@example.test", "b@example.test")
	if err := n.run(context.Background()); err != nil {
		t.Fatalf("run = %v, want nil", err)
	}
	if len(mail.sent) != 2 {
		t.Errorf("sent %d, want both owners tried", len(mail.sent))
	}
}

func TestSurgeCommentOwners(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	users := []*user.User{
		{ID: uuid.New(), Email: "owner@example.test", Role: user.RoleOwner},
		{ID: uuid.New(), Email: "editor@example.test", Role: user.RoleEditor},
		{ID: uuid.New(), Email: "", Role: user.RoleOwner},
		{ID: uuid.New(), Email: "ended@example.test", Role: user.RoleOwner, AccessEndsAt: &past},
	}
	s := &Server{userRepo: moderatorUsers{all: users}}
	got, err := s.commentOwners(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Email != "owner@example.test" {
		t.Errorf("owners = %v, want only the first", got)
	}
}

func TestSurgeHookNeedsItsParts(t *testing.T) {
	if (&Server{}).commentSurgeHook() != nil {
		t.Error("hook built without comments, mail or users")
	}
	s := &Server{comments: &comment.Service{}, emailService: &email.Service{}, userRepo: moderatorUsers{}}
	if s.commentSurgeHook() == nil {
		t.Error("no hook with all parts set")
	}
	t.Setenv("WORKER_DISABLE_JOBS", commentNotifyJob)
	if s.commentSurgeHook() != nil {
		t.Error("hook built although comment_notify is disabled")
	}
}

func TestSurgeAsyncSkipsWhileRunning(t *testing.T) {
	claim := &fakeSurgeClaim{now: time.Now()}
	mail := &fakeSurgeMail{configured: true, block: make(chan struct{})}
	n := newSurgeNotifier(claim, mail, "https://site.test", "a@example.test")
	waitIdle := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for n.running.Load() && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if n.running.Load() {
			t.Fatal("run did not finish")
		}
	}

	n.async(context.Background())
	n.async(context.Background()) // skipped: the first run is blocked in the mailer
	close(mail.block)
	waitIdle()
	if claim.calls != 1 || len(mail.sent) != 1 {
		t.Fatalf("claims = %d, mails = %d; want 1 and 1", claim.calls, len(mail.sent))
	}

	// The flag is released: a call after the first run finished starts a new one.
	mail.block = nil
	n.async(context.Background())
	waitIdle()
	if claim.calls != 2 {
		t.Errorf("claims = %d after the run finished, want 2 (running flag stuck?)", claim.calls)
	}
}
