//go:build integration

package auditlog_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/auditstate"
)

func TestKeyChanges(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	keyA, keyB, creator := uuid.New(), uuid.New(), uuid.NewString()
	tag := "kc-" + uuid.NewString()
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

	insert := func(actorKeyID any, actorID, action, target string, at time.Time) {
		t.Helper()
		_, err := p.Exec(ctx, `INSERT INTO hatamoto_admin_audit (at, actor_id, actor, actor_kind, actor_key_id, action, target, detail, result)
			VALUES ($1, NULLIF($2, '')::uuid, 'api-key:bot', 'api_key', $3, $4, $5, $6::jsonb, 'ok')`,
			at, actorID, actorKeyID, action, target, `{"via":"mcp","tag":"`+tag+`"}`)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert(keyA, creator, "entity.update", tag+"-before", base.Add(-time.Minute)) // before since
	insert(keyA, creator, "entity.update", tag+"-2", base.Add(2*time.Minute))
	insert(keyA, creator, "agent_policy.deny", tag+"-1", base.Add(time.Minute))
	insert(keyB, creator, "entity.update", tag+"-other", base.Add(time.Minute))
	insert(nil, creator, "entity.update", tag+"-session", base.Add(time.Minute)) // the creator's own write, no key

	got, err := auditlog.KeyChanges(ctx, p, keyA, base, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Target != tag+"-1" || got[1].Target != tag+"-2" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Action != "agent_policy.deny" || got[0].Via != "mcp" || got[0].Result != "ok" || got[0].Detail["tag"] != tag ||
		got[0].Actor != "api-key:bot" || got[0].ID == 0 || !got[0].At.Equal(base.Add(time.Minute)) {
		t.Fatalf("first row %+v", got[0])
	}

	one, err := auditlog.KeyChanges(ctx, p, keyA, base, 1)
	if err != nil || len(one) != 1 || one[0].Target != tag+"-1" {
		t.Fatalf("limit: %+v, %v", one, err)
	}

	// A row written through the recorder is found too.
	if err := (auditlog.Recorder{Pool: p}).Record(ctx, nil, auditstate.Event{
		ActorKind: "api_key", Actor: "api-key:bot", KeyID: keyB.String(), Action: "agent_policy.deny", Target: tag + "-rec", Result: "denied"}); err != nil {
		t.Fatal(err)
	}
	rec, err := auditlog.KeyChanges(ctx, p, keyB, base, 10)
	if err != nil || len(rec) != 2 || rec[1].Result != "denied" {
		t.Fatalf("recorder row: %+v, %v", rec, err)
	}
}
