//go:build integration

package server

import (
	"context"
	"encoding/json/jsontext"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/email/smtptest"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/user"
)

// logCapture is an email.Recorder that keeps the entries in memory.
type logCapture struct {
	mu      sync.Mutex
	entries []email.LogEntry
}

func (l *logCapture) Record(_ context.Context, e email.LogEntry) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, e)
	return int64(len(l.entries)), nil
}

func (l *logCapture) kinds() []email.Kind {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []email.Kind
	for _, e := range l.entries {
		out = append(out, e.Kind)
	}
	return out
}

var unsubscribeURLRe = regexp.MustCompile(`https://site\.test(` + regexp.QuoteMeta(commentUnsubscribePrefix) + `[A-Za-z0-9_-]+)`)

// TestCommentNotifyIntegration drives the observers, the notifier job and the
// unsubscribe route against a migrated database and a fake SMTP server.
func TestCommentNotifyIntegration(t *testing.T) {
	ctx := context.Background()
	env := newWireEnv(t, nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := comment.New(env.db, env.cfg.Secret, logger)

	t.Setenv("EMAIL_DISABLED", "")
	srv := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	mailer, err := email.NewService(email.Config{
		SMTPHost: srv.Host(), SMTPPort: srv.Port(), FromAddress: "from@example.test",
		Security: email.SecurityNone, RootCAs: srv.RootCAs(),
	}, logger)
	if err != nil {
		t.Fatal(err)
	}
	logs := &logCapture{}
	mailer.SetRecorder(logs)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	exec := func(q string, args ...any) {
		t.Helper()
		_, err := env.db.Pool().Exec(ctx, q, args...)
		must(err)
	}

	// Moderators: an owner, an editor and an editor whose access ended (gets nothing).
	owner, editor, ended := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO hatamoto_users (id, email, role, name) VALUES ($1, 'owner@cn.test', 'owner', 'Owner'), ($2, 'editor@cn.test', 'editor', 'Editor')`, owner, editor)
	exec(`INSERT INTO hatamoto_users (id, email, role, name, access_ends_at) VALUES ($1, 'ended@cn.test', 'editor', 'Ended', now() - interval '1 day')`, ended)

	entities := entity.NewService(entity.NewPostgresSchemaRepository(env.db), entity.NewPostgresEntityRepository(env.db), logger)
	sch, err := entities.CreateSchema(ctx, &entity.CreateSchemaInput{
		Name: "Cn Page", Slug: "cn-page",
		Schema:   jsontext.Value(`{"type":"object","properties":{}}`),
		Settings: jsontext.Value(`{"url_pattern":"/cn-page/{slug}"}`),
	})
	must(err)
	ent, err := entities.CreateEntity(ctx, nil, &entity.CreateEntityInput{SchemaID: sch.ID, Name: "Cn Entity", Slug: "cn-entity", Attributes: jsontext.Value(`{}`)})
	must(err)

	s := &Server{comments: svc, userRepo: user.NewPostgresRepository(env.db), logger: logger}
	svc.Register(commentNotifyObserver{moderation: s.queueCommentModeration, reply: s.queueCommentReply})
	key := comment.UnsubscribeKey(env.cfg.Secret)
	n := &commentNotifier{
		store: svc, mail: mailer, moderators: s.commentModerators, unsubKey: key, log: logger,
		site: func(context.Context) (string, string, string) { return "Site", "https://site.test", "/admin" },
		entity: func(_ context.Context, id uuid.UUID) (string, string, bool) {
			return "Cn Entity", "/cn-page/cn-entity", id == ent.ID
		},
	}
	run := func() {
		t.Helper()
		must(n.run(ctx))
	}
	setMode := func(m comment.NotifyMode) {
		t.Helper()
		set, err := svc.Settings(ctx)
		must(err)
		set.Enabled, set.NotifyMode = true, m
		must(svc.SaveSettings(ctx, set, nil))
	}
	store := func(mail, name string, status comment.Status, parent *uuid.UUID, notify bool) comment.Comment {
		t.Helper()
		c, err := svc.Store(ctx, comment.Draft{
			EntityID: ent.ID, ParentID: parent, Email: mail, AuthorName: name, BodyRaw: "text", BodyHTML: "<p>text</p>",
			BodyHash: "h-" + uuid.NewString(), SpamVerdict: "allow", Status: status, Locale: "und", NotifyReplies: notify,
		})
		must(err)
		return c
	}
	to := func(addr string) []smtptest.Message {
		var out []smtptest.Message
		for _, m := range srv.Messages() {
			if len(m.To) == 1 && m.To[0] == addr {
				out = append(out, m)
			}
		}
		return out
	}
	backdate := func(d time.Duration) {
		exec(`UPDATE hatamoto_comment_moderator_notify SET last_sent_at = last_sent_at - make_interval(secs => $1)`, d.Seconds())
	}
	total := func() int { return len(srv.Messages()) }

	// A4: moderator mail is throttled per moderator.
	setMode(comment.NotifyPerComment)
	store("a@cn.test", "Ann", comment.StatusPending, nil, false)
	run()
	if len(to("owner@cn.test")) != 1 || len(to("editor@cn.test")) != 1 || len(to("ended@cn.test")) != 0 || total() != 2 {
		t.Fatalf("first run sent %d mails: owner %d, editor %d, ended %d", total(), len(to("owner@cn.test")), len(to("editor@cn.test")), len(to("ended@cn.test")))
	}
	first := to("owner@cn.test")[0].Data
	if !strings.Contains(first, "Subject: New comment waiting on Site") || !strings.Contains(first, "https://site.test/admin/comments?status=pending") {
		t.Errorf("single mail:\n%s", first)
	}
	store("b@cn.test", "Bea", comment.StatusPending, nil, false)
	store("c@cn.test", "Cid", comment.StatusPending, nil, false)
	run()
	if total() != 2 {
		t.Fatalf("a run inside the gap sent %d mails, want none", total()-2)
	}
	backdate(11 * time.Minute)
	run()
	if total() != 4 || !strings.Contains(to("editor@cn.test")[1].Data, "Subject: 2 comments waiting on Site") {
		t.Fatalf("digest run: %d mails total", total())
	}
	if got, want := logs.kinds(), []email.Kind{email.KindCommentModeration, email.KindCommentModeration, email.KindCommentDigest, email.KindCommentDigest}; len(got) != 4 || got[0] != want[0] || got[2] != want[2] || got[3] != want[3] {
		t.Errorf("log kinds = %v, want %v", got, want)
	}
	for _, e := range logs.entries {
		if e.BodyText != "" || e.BodyHTML != "" {
			t.Errorf("log entry keeps a body: %+v", e)
		}
	}

	// Digest mode waits an hour; off clears the queue and sends nothing.
	setMode(comment.NotifyDigest)
	store("d@cn.test", "Dee", comment.StatusPending, nil, false)
	backdate(11 * time.Minute)
	run()
	if total() != 4 {
		t.Fatalf("digest mode sent inside its hour: %d mails", total())
	}
	backdate(time.Hour)
	run()
	if total() != 6 {
		t.Fatalf("digest mode after an hour: %d mails, want 6", total())
	}
	setMode(comment.NotifyOff)
	store("e@cn.test", "Eli", comment.StatusPending, nil, false)
	backdate(2 * time.Hour)
	run()
	var queued int
	must(env.db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_comment_notify_queue`).Scan(&queued))
	if total() != 6 || queued != 0 {
		t.Fatalf("mode off: %d mails, %d queued", total(), queued)
	}

	// A2: reply mail needs the opt-in and a confirmed parent. Moderator mail stays off here.
	parent := store("ada@cn.test", "Ada", comment.StatusDraft, nil, true)
	store("bob@cn.test", "Bob", comment.StatusApproved, &parent.ID, false)
	run()
	if len(to("ada@cn.test")) != 0 {
		t.Fatal("reply mail went to an unconfirmed parent")
	}
	_, err = svc.Confirm(ctx, parent.ID)
	must(err)
	_, err = svc.Approve(ctx, parent.ID, &owner) // the reply mail goes to the author of an approved comment
	must(err)
	store("bob@cn.test", "Eve\r\nBcc: x@y.test", comment.StatusApproved, &parent.ID, false)
	run()
	mails := to("ada@cn.test")
	if len(mails) != 1 {
		t.Fatalf("reply mails = %d, want 1", len(mails))
	}
	head, body, _ := strings.Cut(mails[0].Data, "\r\n\r\n")
	if !strings.Contains(head, "List-Unsubscribe: <https://site.test"+commentUnsubscribePrefix) ||
		!strings.Contains(head, "List-Unsubscribe-Post: List-Unsubscribe=One-Click") {
		t.Errorf("reply headers:\n%s", head)
	}
	if strings.Contains(head, "Bcc:") {
		t.Errorf("a commenter name injected a header:\n%s", head)
	}
	if !strings.Contains(body, "https://site.test/cn-page/cn-entity#comment-") {
		t.Errorf("reply body has no link to the reply:\n%s", body)
	}
	replyUnsub := unsubscribeURLRe.FindStringSubmatch(body)
	if replyUnsub == nil {
		t.Fatalf("reply body has no unsubscribe URL:\n%s", body)
	}
	if last := logs.entries[len(logs.entries)-1]; last.Kind != email.KindCommentReply || strings.Contains(strings.Join(last.Recipients, ","), "ada@cn.test") {
		t.Errorf("reply log entry = %+v", last)
	}

	// A reply by the parent's own commenter is not mailed to them.
	store("ada@cn.test", "Ada", comment.StatusApproved, &parent.ID, false)
	run()
	if len(to("ada@cn.test")) != 1 {
		t.Error("a self-reply was mailed")
	}

	// The unsubscribe route: no Origin, no session, the one-click body.
	post := func(path string) int {
		t.Helper()
		h := commentUnsubscribeHandler(svc, key, n.site, logger)
		r, err := http.NewRequest(http.MethodPost, path, strings.NewReader("List-Unsubscribe=One-Click"))
		must(err)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("token", strings.TrimPrefix(path, commentUnsubscribePrefix))
		w := &statusWriter{header: http.Header{}}
		h(w, r)
		return w.code
	}
	if code := post(replyUnsub[1]); code != http.StatusOK {
		t.Fatalf("unsubscribe POST = %d", code)
	}
	store("bob@cn.test", "Bob", comment.StatusApproved, &parent.ID, false)
	run()
	if len(to("ada@cn.test")) != 1 {
		t.Error("reply mail was sent after the unsubscribe")
	}

	// A moderator's link mutes only that moderator.
	setMode(comment.NotifyPerComment)
	store("f@cn.test", "Fay", comment.StatusPending, nil, false)
	backdate(time.Hour)
	run()
	modUnsub := unsubscribeURLRe.FindStringSubmatch(to("owner@cn.test")[len(to("owner@cn.test"))-1].Data)
	if modUnsub == nil {
		t.Fatal("moderator mail has no unsubscribe URL")
	}
	if code := post(modUnsub[1]); code != http.StatusOK {
		t.Fatalf("moderator unsubscribe POST = %d", code)
	}
	ownerBefore, editorBefore := len(to("owner@cn.test")), len(to("editor@cn.test"))
	store("g@cn.test", "Gus", comment.StatusPending, nil, false)
	backdate(time.Hour)
	run()
	if len(to("owner@cn.test")) != ownerBefore || len(to("editor@cn.test")) != editorBefore+1 {
		t.Errorf("after muting the owner: owner %d -> %d, editor %d -> %d", ownerBefore, len(to("owner@cn.test")), editorBefore, len(to("editor@cn.test")))
	}
}

// statusWriter is the smallest http.ResponseWriter that keeps a status code.
type statusWriter struct {
	header http.Header
	code   int
}

func (w *statusWriter) Header() http.Header         { return w.header }
func (w *statusWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *statusWriter) WriteHeader(code int)        { w.code = code }

// TestCommentNotifyQueuesWithoutWorker: a replica that runs no worker (here the
// wire environment sets HATAMOTO_WORKER=off) still queues mail work, so a
// replica that does run the job can send it.
func TestCommentNotifyQueuesWithoutWorker(t *testing.T) {
	ctx := context.Background()
	env := newWireEnv(t, nil)
	s, _ := env.server(t, "/admin")
	env.addOwner(t, s)
	if s.scheduler != nil {
		t.Fatal("test needs a server without a scheduler")
	}
	entities := entity.NewService(entity.NewPostgresSchemaRepository(env.db), entity.NewPostgresEntityRepository(env.db), s.logger)
	sch, err := entities.CreateSchema(ctx, &entity.CreateSchemaInput{
		Name: "Nw Page", Slug: "nw-page",
		Schema:   jsontext.Value(`{"type":"object","properties":{}}`),
		Settings: jsontext.Value(`{"url_pattern":"/nw-page/{slug}"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	ent, err := entities.CreateEntity(ctx, nil, &entity.CreateEntityInput{SchemaID: sch.ID, Name: "Nw Entity", Slug: "nw-entity", Attributes: jsontext.Value(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.comments.Store(ctx, comment.Draft{
		EntityID: ent.ID, Email: "a@nw.test", AuthorName: "Ann", BodyRaw: "text", BodyHTML: "<p>text</p>",
		BodyHash: "h-" + uuid.NewString(), SpamVerdict: "allow", Status: comment.StatusPending, Locale: "und",
	}); err != nil {
		t.Fatal(err)
	}
	var queued int
	if err := env.db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_comment_notify_queue`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 1 {
		t.Fatalf("queue rows = %d, want 1 on a replica without the worker", queued)
	}
}
