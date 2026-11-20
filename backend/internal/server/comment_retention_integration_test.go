//go:build integration

package server

import (
	"context"
	"encoding/json/jsontext"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/entity"
)

// TestCommentRetentionIntegration runs the comment purges against a migrated
// database: the hourly drafts purge and the two daily retention purges.
func TestCommentRetentionIntegration(t *testing.T) {
	ctx := context.Background()
	env := newWireEnv(t, nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := comment.New(env.db, env.cfg.Secret, logger)
	idsvc, err := identity.New(identity.Config{
		DB: env.db, Comments: svc, Mailer: nopMailer{},
		Site:   func(context.Context) (string, string) { return "Site", "https://site.test" },
		Secret: env.cfg.Secret, Now: time.Now, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{comments: svc, commentIdentity: idsvc, logger: logger}

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
	exists := func(table, col string, id any) bool {
		t.Helper()
		var n int
		must(env.db.Pool().QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE `+col+` = $1`, id).Scan(&n))
		return n == 1
	}

	entities := entity.NewService(entity.NewPostgresSchemaRepository(env.db), entity.NewPostgresEntityRepository(env.db), logger)
	sch, err := entities.CreateSchema(ctx, &entity.CreateSchemaInput{
		Name: "Ret Page", Slug: "ret-page",
		Schema:   jsontext.Value(`{"type":"object","properties":{}}`),
		Settings: jsontext.Value(`{"url_pattern":"/ret-page/{slug}"}`),
	})
	must(err)
	ent, err := entities.CreateEntity(ctx, nil, &entity.CreateEntityInput{SchemaID: sch.ID, Name: "Ret Entity", Slug: "ret-entity", Attributes: jsontext.Value(`{}`)})
	must(err)

	store := func(mail string, parent *uuid.UUID) comment.Comment {
		t.Helper()
		c, err := svc.Store(ctx, comment.Draft{
			EntityID: ent.ID, ParentID: parent, Email: mail, AuthorName: "Ret", BodyRaw: "text", BodyHTML: "<p>text</p>",
			BodyHash: "h", SpamVerdict: "allow", Status: comment.StatusDraft, Locale: "und",
		})
		must(err)
		return c
	}
	setStatus := func(id uuid.UUID, status string, days int) {
		exec(`UPDATE hatamoto_comments SET status = $2, updated_at = now() - make_interval(days => $3) WHERE id = $1`, id, status, days)
	}

	// Hourly drafts purge: an unconfirmed draft older than a day goes (with its
	// draft-only commenter); a moderator-approved one (confirmed_at NULL) stays.
	oldDraft := store("old-draft@ret.test", nil)
	exec(`UPDATE hatamoto_comments SET created_at = now() - interval '25 hours' WHERE id = $1`, oldDraft.ID)
	exec(`UPDATE hatamoto_commenters SET last_seen_at = now() - interval '25 hours' WHERE id = $1`, *oldDraft.CommenterID)
	modApproved := store("mod-approved@ret.test", nil)
	setStatus(modApproved.ID, "approved", 0)
	exec(`UPDATE hatamoto_comments SET created_at = now() - interval '25 hours' WHERE id = $1`, modApproved.ID)

	// Daily purges: old and young spam, an old trash parent that has an approved reply.
	oldSpam := store("spam@ret.test", nil)
	setStatus(oldSpam.ID, "spam", 31)
	youngSpam := store("young-spam@ret.test", nil)
	setStatus(youngSpam.ID, "spam", 2)
	trash := store("trash@ret.test", nil)
	reply := store("reply@ret.test", &trash.ID)
	setStatus(reply.ID, "approved", 0)
	setStatus(trash.ID, "trash", 31)

	// Form tokens: one expired, one still valid.
	exec(`INSERT INTO hatamoto_comment_form_tokens (token_id, expires_at) VALUES ('ret-expired', now() - interval '1 hour'), ('ret-fresh', now() + interval '1 hour')`)

	must(s.purgeCommentDrafts(ctx))
	var rec recordingAdder
	s.addCommentRetention(&rec)
	for _, p := range rec.fns {
		_, err := p(ctx)
		must(err)
	}

	if exists("hatamoto_comments", "id", oldDraft.ID) {
		t.Error("the old draft survived")
	}
	if exists("hatamoto_commenters", "id", *oldDraft.CommenterID) {
		t.Error("the draft-only commenter survived")
	}
	if !exists("hatamoto_comments", "id", modApproved.ID) {
		t.Error("the moderator-approved comment was purged")
	}
	if exists("hatamoto_comments", "id", oldSpam.ID) {
		t.Error("the old spam survived")
	}
	if !exists("hatamoto_comments", "id", youngSpam.ID) {
		t.Error("the young spam was purged")
	}
	var body string
	must(env.db.Pool().QueryRow(ctx, `SELECT body_raw FROM hatamoto_comments WHERE id = $1`, trash.ID).Scan(&body))
	if body == "text" {
		t.Error("the trash parent with a reply was not blanked")
	}
	if !exists("hatamoto_comments", "id", reply.ID) {
		t.Error("the approved reply was purged with its parent")
	}
	if exists("hatamoto_comment_form_tokens", "token_id", "ret-expired") || !exists("hatamoto_comment_form_tokens", "token_id", "ret-fresh") {
		t.Error("form tokens: the expired one must go and the fresh one stay")
	}
}
