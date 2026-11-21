package entity

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/auditctx"
	"hatamoto.dev/core/internal/auditstate"
)

func TestAuditStatusAction(t *testing.T) {
	t.Parallel()
	for st, want := range map[EntityStatus]string{
		StatusPublished: "entity.publish", StatusArchived: "entity.archive", StatusDraft: "entity.unpublish",
	} {
		if got := auditStatusAction(st); got != want {
			t.Errorf("auditStatusAction(%q) = %q, want %q", st, got, want)
		}
	}
}

func TestAuditActorGate(t *testing.T) {
	t.Parallel()
	withActor := auditctx.With(context.Background(), auditctx.Actor{Kind: "api_key", Name: "api-key:x"})
	r := &PostgresEntityRepository{}
	if _, ok := r.auditActor(withActor); ok {
		t.Fatal("no hook set, yet audited")
	}
	r.SetAuditTx(func(context.Context, pgx.Tx, auditstate.Event) error { return nil })
	if _, ok := r.auditActor(context.Background()); ok {
		t.Fatal("no actor, yet audited")
	}
	if _, ok := r.auditActor(withActor); !ok {
		t.Fatal("hook and actor set, not audited")
	}
}

func TestAuditEventDetail(t *testing.T) {
	t.Parallel()
	allow := map[string]bool{"name": true, "slug": true, "status": true, "to": true, "from": true, "publish_at": true,
		"took_ownership": true, "via": true, "schema": true, "bulk": true, "locale": true, "builder": true}
	id := uuid.New()
	a := auditctx.Actor{Kind: "api_key", Name: "api-key:x", KeyID: "k", Via: "api", Detail: map[string]any{"schema": "page", "bulk": true, "name": "ignored"}}
	ev := auditEvent(a, auditUpdate, id, map[string]any{"name": "N", "slug": "s", "took_ownership": true})
	for k := range ev.Detail {
		if !allow[k] {
			t.Errorf("detail key %q outside the allow-list", k)
		}
	}
	if ev.Detail["name"] != "N" || ev.Detail["schema"] != "page" || ev.Detail["via"] != "api" || ev.Target != id.String() || ev.KeyID != "k" {
		t.Fatalf("event = %+v", ev)
	}
	if _, ok := auditEvent(auditctx.Actor{Kind: "user"}, auditCreate, id, map[string]any{}).Detail["via"]; ok {
		t.Fatal("via set for an empty Via")
	}
}
