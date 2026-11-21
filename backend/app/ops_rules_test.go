//go:build integration

package app

import (
	"bytes"

	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/user"
)

func TestOpsRulesCLIAs(t *testing.T) {
	ctx, db := cliAuditDB(t)
	logger := quietLogger()
	repo := user.NewPostgresRepository(db)
	for email, role := range map[string]user.Role{"writer@example.test": user.RoleContributor, "boss@example.test": user.RoleOwner} {
		if _, err := repo.Create(ctx, &user.CreateUserInput{Email: email, Role: role}, "hash"); err != nil {
			t.Fatal(err)
		}
	}
	status := func(id string) string {
		var s string
		if err := db.Pool().QueryRow(ctx, `SELECT status::text FROM hatamoto_entities WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	create := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runEntity(ctx, db, append([]string{"create", "--schema", "page"}, args...), &out, logger)
		var e struct {
			ID string `json:"id"`
		}
		_ = jsonx.Unmarshal(out.Bytes(), &e)
		return e.ID, err
	}

	// A contributor cannot publish: the entry stays a draft.
	id, err := create("--name", "Draft one", "--as", "writer@example.test")
	if err != nil || id == "" {
		t.Fatalf("contributor create: %v", err)
	}
	err = runEntity(ctx, db, []string{"publish", id, "--as", "writer@example.test"}, &bytes.Buffer{}, logger)
	if err == nil || !strings.Contains(err.Error(), "cannot publish") {
		t.Fatalf("contributor publish err = %v, want a refusal", err)
	}
	if got := status(id); got != "draft" {
		t.Fatalf("status after refused publish = %q", got)
	}

	// create --publish as a contributor leaves the created draft and reports the refusal.
	_, err = create("--name", "Draft two", "--publish", "--as", "writer@example.test")
	if err == nil || !strings.Contains(err.Error(), "cannot publish") {
		t.Fatalf("contributor create --publish err = %v, want a refusal", err)
	}
	var n int
	if e := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities WHERE name = 'Draft two' AND status = 'draft'`).Scan(&n); e != nil || n != 1 {
		t.Fatalf("draft rows = %d, %v", n, e)
	}

	// No --as is the system: it publishes.
	id, err = create("--name", "System one", "--publish")
	if err != nil || status(id) != "published" {
		t.Fatalf("system create --publish: %v", err)
	}

	// An owner publishes.
	id, err = create("--name", "Owner one", "--publish", "--as", "boss@example.test")
	if err != nil || status(id) != "published" {
		t.Fatalf("owner create --publish: %v", err)
	}
}
