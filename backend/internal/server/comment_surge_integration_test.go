//go:build integration

package server

import (
	"context"
	"encoding/json/jsontext"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/email/smtptest"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/user"
)

// TestCommentSurgeIntegration sends the surge mail through a migrated database
// and a fake SMTP server: one mail per episode, owners only, no comment data.
func TestCommentSurgeIntegration(t *testing.T) {
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

	exec(`INSERT INTO hatamoto_users (id, email, role, name) VALUES ($1, 'owner@cs.test', 'owner', 'Owner'), ($2, 'editor@cs.test', 'editor', 'Editor')`, uuid.New(), uuid.New())
	exec(`INSERT INTO hatamoto_users (id, email, role, name, access_ends_at) VALUES ($1, 'ended@cs.test', 'owner', 'Ended', now() - interval '1 day')`, uuid.New())

	entities := entity.NewService(entity.NewPostgresSchemaRepository(env.db), entity.NewPostgresEntityRepository(env.db), logger)
	sch, err := entities.CreateSchema(ctx, &entity.CreateSchemaInput{
		Name: "Cs Page", Slug: "cs-page",
		Schema:   jsontext.Value(`{"type":"object","properties":{}}`),
		Settings: jsontext.Value(`{"url_pattern":"/cs-page/{slug}"}`),
	})
	must(err)
	ent, err := entities.CreateEntity(ctx, nil, &entity.CreateEntityInput{SchemaID: sch.ID, Name: "Cs Entity", Slug: "cs-entity", Attributes: jsontext.Value(`{}`)})
	must(err)

	// Commenter data exists in the database; none of it may reach the mail.
	_, err = svc.Store(ctx, comment.Draft{
		EntityID: ent.ID, Email: "reader@cs.test", AuthorName: "Zed Surgeson",
		BodyRaw: "surge-body-marker", BodyHTML: "<p>surge-body-marker</p>",
		BodyHash: "h-" + uuid.NewString(), SpamVerdict: "allow", Status: comment.StatusPending, Locale: "und",
	})
	must(err)

	s := &Server{comments: svc, userRepo: user.NewPostgresRepository(env.db), logger: logger}
	n := &commentSurgeNotifier{
		claim: svc, mail: mailer, owners: s.commentOwners, log: logger,
		site: func(context.Context) (string, string, string) { return "Site", "https://site.test", "/admin" },
	}
	must(n.run(ctx))
	must(n.run(ctx))

	msgs := srv.Messages()
	if len(msgs) != 1 || len(msgs[0].To) != 1 || msgs[0].To[0] != "owner@cs.test" {
		t.Fatalf("messages = %+v, want exactly one, to owner@cs.test", msgs)
	}
	for _, marker := range []string{"Zed Surgeson", "surge-body-marker", "reader@cs.test"} {
		if strings.Contains(msgs[0].Data, marker) {
			t.Errorf("surge mail contains %q", marker)
		}
	}
	if !strings.Contains(msgs[0].Data, "https://site.test/admin/comments?status=pending") {
		t.Error("surge mail has no link to the pending queue")
	}
	if kinds := logs.kinds(); len(kinds) != 1 || kinds[0] != email.KindCommentSurge {
		t.Errorf("logged kinds = %v, want [comment_surge]", kinds)
	}
	for _, e := range logs.entries {
		if e.BodyText != "" || e.BodyHTML != "" {
			t.Error("the email log kept a body")
		}
	}
}
