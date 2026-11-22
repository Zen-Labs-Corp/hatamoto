//go:build integration

package reviewflow_test

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/reviewflow"
	"hatamoto.dev/core/internal/reviewstate"
	"hatamoto.dev/core/internal/user"
)

type switchOn struct{}

func (switchOn) ReviewEnabled(context.Context) (bool, error) { return true, nil }

type sentMail struct {
	to string
	d  email.ReviewDecisionData
}

type mailLog struct {
	mu   sync.Mutex
	sent []sentMail
}

func (m *mailLog) SendReviewDecision(_ context.Context, to string, d email.ReviewDecisionData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, sentMail{to, d})
	return nil
}

func (m *mailLog) outcomes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, s := range m.sent {
		out = append(out, s.d.Outcome)
	}
	return out
}

type fixture struct {
	t      *testing.T
	db     *database.DB
	ent    *entity.Service
	eng    *changeset.Engine
	flow   *reviewflow.Flow
	mail   *mailLog
	schema *entity.EntitySchema
	author uuid.UUID
	editor uuid.UUID
	owner  uuid.UUID
	tag    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 6, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	schemas := entity.NewPostgresSchemaRepository(db)
	tag := uuid.NewString()[:8]
	sc, err := schemas.Create(ctx, &entity.CreateSchemaInput{Name: "rf " + tag, Slug: "rf-" + tag,
		Schema: jsontext.Value(`{"type":"object","properties":{"content":{"type":"string"}},"required":["content"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	ent := entity.NewService(schemas, entity.NewPostgresEntityRepository(db), logger)
	mediaSvc := media.NewService(media.NewPostgresRepository(db), media.Config{UploadDir: t.TempDir(), BaseURL: "/media", MaxSizeMB: 10}, logger)
	eng := &changeset.Engine{DB: db, Blobs: mediaSvc, Logger: logger,
		Writers: map[changeset.Kind]changeset.KindWriter{changeset.KindEntity: changeset.EntityWriter{Svc: ent}}}

	f := &fixture{t: t, db: db, ent: ent, eng: eng, mail: &mailLog{}, schema: sc, tag: tag}
	repo := user.NewPostgresRepository(db)
	mk := func(label string, role user.Role, name string) uuid.UUID {
		u, err := repo.Create(ctx, &user.CreateUserInput{Email: label + "-" + tag + "@example.test", Role: role, Name: name}, "x")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_users WHERE id = $1`, u.ID) })
		return u.ID
	}
	f.author = mk("author", user.RoleAuthor, "Ann Author")
	f.editor = mk("editor", user.RoleEditor, "Ed Editor")
	f.owner = mk("owner", user.RoleOwner, "")
	us := repo
	f.flow = &reviewflow.Flow{Switch: switchOn{}, Engine: eng, Entities: ent, Users: us, Mail: f.mail,
		Site: func(context.Context) (string, string) { return "Site", "https://example.test/admin" }, Logger: logger}
	ent.SetReviewGate(f.flow)

	reviewstate.Reset()
	t.Cleanup(reviewstate.Reset)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = db.Pool().Exec(bg, `DELETE FROM hatamoto_changesets WHERE id IN (
			SELECT changeset_id FROM hatamoto_changeset_items WHERE target_id IN (SELECT id::text FROM hatamoto_entities WHERE schema_id = $1))`, sc.ID)
		_, _ = db.Pool().Exec(bg, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, sc.ID)
		_ = schemas.Delete(bg, sc.ID)
	})
	return f
}

func (f *fixture) as(id uuid.UUID, role string) context.Context {
	return authzctx.With(context.Background(), authz.Subject{UserID: id.String(), Role: role})
}

// publishedBy makes a published entry created by creator, written as the system.
func (f *fixture) publishedBy(creator uuid.UUID, draftOnly bool) *entity.Entity {
	f.t.Helper()
	ctx := context.Background()
	e, err := f.ent.CreateEntity(ctx, &creator, &entity.CreateEntityInput{SchemaID: f.schema.ID, Name: "Page",
		Slug: "page-" + uuid.NewString()[:8], Attributes: jsontext.Value(`{"content":"one"}`)})
	if err != nil {
		f.t.Fatal(err)
	}
	if draftOnly {
		return e
	}
	if e, err = f.ent.UpdateEntityStatus(ctx, e.ID, &creator, entity.StatusPublished); err != nil {
		f.t.Fatal(err)
	}
	return e
}

func attrs(s string) *entity.UpdateEntityInput {
	v := jsontext.Value(`{"content":"` + s + `"}`)
	return &entity.UpdateEntityInput{Attributes: &v}
}

// sendEdit stages the author's edit of e and returns the changeset id.
func (f *fixture) sendEdit(e *entity.Entity, content string) int64 {
	f.t.Helper()
	_, err := f.ent.UpdateEntity(f.as(f.author, "author"), e.ID, &f.author, attrs(content))
	var sent *entity.SentForReviewError
	if !errors.As(err, &sent) {
		f.t.Fatalf("UpdateEntity err = %v, want a SentForReviewError", err)
	}
	return sent.ChangesetID
}

// content is the live entry's "content" attribute.
func (f *fixture) content(e *entity.Entity) string {
	f.t.Helper()
	live, err := f.ent.GetEntity(context.Background(), e.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	var a struct {
		Content string `json:"content"`
	}
	if err := jsonx.Unmarshal(live.Attributes, &a); err != nil {
		f.t.Fatal(err)
	}
	return a.Content
}

func TestApprovePublishesAndMails(t *testing.T) {
	f := newFixture(t)
	e := f.publishedBy(f.author, false)
	cs := f.sendEdit(e, "two")
	if got := f.content(e); got != "one" {
		t.Fatalf("live changed before approval: %s", got)
	}

	out, err := f.flow.Approve(f.as(f.editor, "editor"), cs, "looks good")
	if err != nil || !out.Published || out.NotApplied {
		t.Fatalf("Approve = %+v, %v; want published", out, err)
	}
	if got := f.content(e); got != "two" {
		t.Fatalf("live after approval = %s", got)
	}
	if got := f.mail.outcomes(); len(got) != 1 || got[0] != email.ReviewApproved {
		t.Fatalf("mail = %v, want one approved", got)
	}
	m := f.mail.sent[0]
	if m.to != "author-"+f.tag+"@example.test" || m.d.Reviewer != "Ed Editor" || m.d.EntryName != "Page" || m.d.URL == "" {
		t.Fatalf("mail = %+v", m)
	}
	if _, ok, _ := f.eng.PendingEdit(context.Background(), e.ID.String()); ok {
		// PendingEdit reads staged rows only; a published edit is no longer staged.
		t.Fatal("the published edit is still pending")
	}
}

func TestApproveRefusesTheRequester(t *testing.T) {
	f := newFixture(t)
	e := f.publishedBy(f.author, false)
	cs := f.sendEdit(e, "two")
	// Even an owner-role subject cannot approve their own change.
	_, err := f.flow.Approve(f.as(f.author, "owner"), cs, "")
	if !errors.Is(err, changeset.ErrSelfDecision) {
		t.Fatalf("err = %v, want ErrSelfDecision", err)
	}
	if got := f.content(e); got != "one" {
		t.Fatalf("live changed: %s", got)
	}
	// An author may not decide at all.
	if _, err := f.flow.Approve(f.as(uuid.New(), "author"), cs, ""); !errors.Is(err, reviewflow.ErrNotDecider) {
		t.Fatalf("author err = %v, want ErrNotDecider", err)
	}
}

func TestApproveStaleKeepsTheEditPending(t *testing.T) {
	f := newFixture(t)
	e := f.publishedBy(f.author, false)
	cs := f.sendEdit(e, "two")
	// The owner edits live in the meantime (exempt, so written directly).
	if _, err := f.ent.UpdateEntity(f.as(f.owner, "owner"), e.ID, &f.owner, attrs("owner")); err != nil {
		t.Fatal(err)
	}
	_, err := f.flow.Approve(f.as(f.editor, "editor"), cs, "")
	if !errors.Is(err, reviewflow.ErrEditStale) {
		t.Fatalf("err = %v, want ErrEditStale", err)
	}
	if ds, _ := f.eng.StageDecisions(context.Background(), cs); len(ds) != 0 {
		t.Fatalf("decisions = %+v, want none", ds)
	}
	row, ok, err := f.eng.PendingEdit(context.Background(), e.ID.String())
	if err != nil || !ok || row.ID != cs || row.Rejected {
		t.Fatalf("PendingEdit = %+v, %v, %v; want the edit still pending", row, ok, err)
	}
	if got := f.content(e); got != "owner" {
		t.Fatalf("live = %s, want the owner's change", got)
	}
	if len(f.mail.outcomes()) != 0 {
		t.Fatalf("mail = %v, want none", f.mail.outcomes())
	}
}

func TestSendBackKeepsTheVersion(t *testing.T) {
	f := newFixture(t)
	e := f.publishedBy(f.author, false)
	cs := f.sendEdit(e, "two")
	ed := f.as(f.editor, "editor")

	if err := f.flow.SendBack(ed, cs, "  "); !errors.Is(err, reviewflow.ErrCommentNeeded) {
		t.Fatalf("no comment: err = %v", err)
	}
	if err := f.flow.SendBack(ed, cs, "Fix the heading"); err != nil {
		t.Fatal(err)
	}
	row, ok, err := f.eng.PendingEdit(context.Background(), e.ID.String())
	if err != nil || !ok || !row.Rejected || row.State != changeset.StateStaged {
		t.Fatalf("PendingEdit = %+v, %v, %v; want a staged, rejected edit", row, ok, err)
	}
	if _, items, err := f.eng.Get(context.Background(), cs); err != nil || len(items) != 1 {
		t.Fatalf("items = %d, %v; want the payload kept", len(items), err)
	}
	if queue, _ := f.eng.ListEdits(context.Background(), changeset.EditFilter{Pending: true, TargetID: e.ID.String()}, 10, 0); len(queue) != 0 {
		t.Fatalf("a sent-back edit is still in the queue: %+v", queue)
	}
	if got := f.mail.outcomes(); len(got) != 1 || got[0] != email.ReviewSentBack || f.mail.sent[0].d.Comment != "Fix the heading" {
		t.Fatalf("mail = %+v", f.mail.sent)
	}
	if got := f.content(e); got != "one" {
		t.Fatalf("live changed: %s", got)
	}
	// A sent-back edit cannot be approved.
	if _, err := f.flow.Approve(ed, cs, ""); !errors.Is(err, changeset.ErrStageRejected) {
		t.Fatalf("approve after send back: err = %v", err)
	}
}

func TestWithdrawAndDiscard(t *testing.T) {
	f := newFixture(t)
	e := f.publishedBy(f.author, false)
	cs := f.sendEdit(e, "two")

	if err := f.flow.Withdraw(f.as(f.editor, "editor"), cs); !errors.Is(err, reviewflow.ErrNotRequester) {
		t.Fatalf("Withdraw by another: err = %v, want ErrNotRequester", err)
	}
	if err := f.flow.Discard(f.as(f.author, "author"), cs); !errors.Is(err, reviewflow.ErrNotDecider) {
		t.Fatalf("Discard by an author: err = %v, want ErrNotDecider", err)
	}
	if err := f.flow.Withdraw(f.as(f.author, "author"), cs); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := f.eng.PendingEdit(context.Background(), e.ID.String()); ok {
		t.Fatal("a withdrawn edit is still pending")
	}

	cs = f.sendEdit(e, "three")
	if err := f.flow.Discard(f.as(f.owner, "owner"), cs); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownGroupCannotBeDecided(t *testing.T) {
	f := newFixture(t)
	reviewstate.Register(legalPolicy{})
	e := f.publishedBy(f.author, false)
	cs := f.sendEdit(e, "two")
	_, err := f.flow.Approve(f.as(f.owner, "owner"), cs, "")
	if !errors.Is(err, reviewflow.ErrUnknownGroup) {
		t.Fatalf("Approve err = %v, want ErrUnknownGroup", err)
	}
	if err := f.flow.SendBack(f.as(f.owner, "owner"), cs, "no"); !errors.Is(err, reviewflow.ErrUnknownGroup) {
		t.Fatalf("SendBack err = %v, want ErrUnknownGroup", err)
	}
	// An owner can still discard it.
	if err := f.flow.Discard(f.as(f.owner, "owner"), cs); err != nil {
		t.Fatal(err)
	}
}

type legalPolicy struct{}

func (legalPolicy) Stages(context.Context, reviewstate.Edit) ([]reviewstate.Stage, error) {
	return []reviewstate.Stage{{Name: "Legal", Group: "legal"}}, nil
}

func TestPublishNeedsEveryStageApproved(t *testing.T) {
	f := newFixture(t)
	e := f.publishedBy(f.author, false)
	cs := f.sendEdit(e, "two")
	_, err := f.flow.Publish(f.as(f.editor, "editor"), cs)
	if !errors.Is(err, changeset.ErrStagesPending) {
		t.Fatalf("Publish err = %v, want ErrStagesPending", err)
	}
}

func TestScheduledRequestIsApprovedAsScheduled(t *testing.T) {
	f := newFixture(t)
	d := f.publishedBy(f.author, true)
	at := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	_, err := f.ent.ScheduleEntity(f.as(f.author, "author"), d.ID, &f.author, at)
	var sent *entity.SentForReviewError
	if !errors.As(err, &sent) {
		t.Fatalf("ScheduleEntity err = %v, want a SentForReviewError", err)
	}
	live, _ := f.ent.GetEntity(context.Background(), d.ID)
	if live.Status != entity.StatusDraft {
		t.Fatalf("status before approval = %s, want draft", live.Status)
	}
	if out, err := f.flow.Approve(f.as(f.editor, "editor"), sent.ChangesetID, ""); err != nil || !out.Published {
		t.Fatalf("Approve = %+v, %v", out, err)
	}
	live, _ = f.ent.GetEntity(context.Background(), d.ID)
	if live.Status != entity.StatusScheduled || live.PublishedAt == nil || !live.PublishedAt.Equal(at) {
		t.Fatalf("after approval status = %s, published_at = %v; want scheduled at %v", live.Status, live.PublishedAt, at)
	}
}

// Approving "publish now" of a scheduled entry publishes it now, as the direct
// path does, not at the scheduled time.
func TestApprovedPublishNowResetsAFuturePublishedAt(t *testing.T) {
	f := newFixture(t)
	d := f.publishedBy(f.author, true)
	at := time.Now().UTC().Add(72 * time.Hour)
	if _, err := f.ent.ScheduleEntity(context.Background(), d.ID, &f.author, at); err != nil {
		t.Fatal(err)
	}
	_, err := f.ent.UpdateEntityStatus(f.as(f.author, "author"), d.ID, &f.author, entity.StatusPublished)
	var sent *entity.SentForReviewError
	if !errors.As(err, &sent) {
		t.Fatalf("UpdateEntityStatus err = %v, want a SentForReviewError", err)
	}
	if out, err := f.flow.Approve(f.as(f.editor, "editor"), sent.ChangesetID, ""); err != nil || !out.Published {
		t.Fatalf("Approve = %+v, %v", out, err)
	}
	live, _ := f.ent.GetEntity(context.Background(), d.ID)
	if live.Status != entity.StatusPublished || live.PublishedAt == nil || live.PublishedAt.After(time.Now().Add(time.Minute)) {
		t.Fatalf("after approval status = %s, published_at = %v; want published now", live.Status, live.PublishedAt)
	}
}
