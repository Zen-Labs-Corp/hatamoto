//go:build integration

package app

import (
	"bytes"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/user"
)

// With the site's review switch on, a publish made --as an author is staged and
// the command succeeds with a message; the live entry is untouched. Without --as
// the CLI is the system and publishes directly.
func TestOpsReviewCLIAs(t *testing.T) {
	ctx, db := cliAuditDB(t)
	logger := quietLogger()
	if _, err := user.NewPostgresRepository(db).Create(ctx, &user.CreateUserInput{Email: "writer@example.test", Role: user.RoleAuthor}, "hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_site_config (id, company_name) VALUES (TRUE, 'Site') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err := siteconfig.NewStore(db, logger).SetReviewEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	status := func(id string) string {
		var s string
		if err := db.Pool().QueryRow(ctx, `SELECT status::text FROM hatamoto_entities WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	create := func(name string, extra ...string) (string, string) {
		var out bytes.Buffer
		args := append([]string{"create", "--schema", "page", "--name", name}, extra...)
		if err := runEntity(ctx, db, args, &out, logger); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		var e struct {
			ID string `json:"id"`
		}
		if err := jsonx.Unmarshal(out.Bytes()[:strings.Index(out.String(), "\n}\n")+2], &e); err != nil {
			t.Fatalf("create output %q: %v", out.String(), err)
		}
		return e.ID, out.String()
	}

	id, _ := create("Mine", "--as", "writer@example.test")
	var out bytes.Buffer
	if err := runEntity(ctx, db, []string{"publish", id, "--as", "writer@example.test"}, &out, logger); err != nil {
		t.Fatalf("publish --as: %v", err)
	}
	if !strings.HasPrefix(out.String(), "Sent for review (change ") || !strings.Contains(out.String(), "goes live when a reviewer approves it") {
		t.Fatalf("output = %q", out.String())
	}
	if got := status(id); got != "draft" {
		t.Fatalf("status after a staged publish = %q, want draft", got)
	}
	var staged int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_changesets WHERE kind = 'edit' AND state = 'staged'`).Scan(&staged); err != nil || staged != 1 {
		t.Fatalf("staged edits = %d, %v; want 1", staged, err)
	}
	var audited int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'changeset.edit_submit' AND actor = 'writer@example.test'`).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("edit_submit rows by the user = %d, %v; want 1", audited, err)
	}

	// create --publish as the author: the draft is created, its publish is staged.
	_, text := create("Second", "--publish", "--as", "writer@example.test")
	if !strings.Contains(text, "Sent for review (change ") {
		t.Fatalf("create --publish output = %q", text)
	}

	// The CLI without --as is the system: never held.
	out.Reset()
	if err := runEntity(ctx, db, []string{"publish", id}, &out, logger); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Sent for review") || status(id) != "published" {
		t.Fatalf("system publish: status %q, output %q", status(id), out.String())
	}
}
