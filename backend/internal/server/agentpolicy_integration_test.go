//go:build integration

package server

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hatamoto.dev/core/agentpolicy"
	"hatamoto.dev/core/internal/agentpolicystate"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/authzsubject"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

// An API key's writes to live content, through the production wiring of a full
// Server on a migrated Postgres, with one fake policy that answers per key:
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run AgentPolicy

// apFake answers by key id, so cases that share the registered policy do not
// leak into each other. A key it does not know is allowed.
type apFake struct {
	mu sync.Mutex
	by map[string]agentpolicy.Decision
}

func (f *apFake) Decide(_ context.Context, r agentpolicy.Request) (agentpolicy.Decision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.by[r.KeyID]; ok {
		return d, nil
	}
	return agentpolicy.Allow(), nil
}

func (f *apFake) set(keyID string, d agentpolicy.Decision) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.by[keyID] = d
}

type apEnv struct {
	*entAuditEnv
	fake       *apFake
	start      time.Time
	requesters []string
}

// apNew boots the server and registers the fake policy (nothing is registered
// when register is false). Registration is undone when the test ends.
func apNew(t *testing.T, register bool) *apEnv {
	t.Helper()
	e := &apEnv{entAuditEnv: entAuditNew(t), fake: &apFake{by: map[string]agentpolicy.Decision{}}, start: time.Now().Add(-time.Second)}
	agentpolicystate.Reset()
	e.srv.wireAgentPolicy() // Reset drops the runtime New installed
	if register {
		agentpolicy.Register(e.fake)
	}
	t.Cleanup(agentpolicystate.Reset)
	t.Cleanup(func() {
		// the staged edits of this test's keys, and their runs (items cascade)
		_, _ = e.db.Pool().Exec(context.Background(),
			`DELETE FROM hatamoto_runs WHERE id IN (SELECT run_id FROM hatamoto_changesets WHERE kind = 'edit' AND requested_by LIKE 'api-key:%' AND requested_by = ANY($1))`, e.requesters)
	})
	return e
}

func (e *apEnv) writeKey(name string) (*auth.APIKeyWithSecret, string) {
	e.t.Helper()
	k := e.key(name, auth.ScopeWriteEntities, auth.ScopeReadEntities)
	e.requesters = append(e.requesters, "api-key:"+k.ID.String())
	return k, k.ID.String()
}

// entity makes an entry written by the owner directly (no key, no review).
func (e *apEnv) entity(slug string, published bool) *entity.Entity {
	e.t.Helper()
	ent, err := e.srv.entityService.CreateEntity(e.ctx, &e.owner.ID, &entity.CreateEntityInput{
		SchemaID: e.schemaID, Name: "Note " + slug, Slug: slug + "-" + e.sfx, Attributes: []byte(`{}`)})
	if err != nil {
		e.t.Fatal(err)
	}
	if published {
		if _, err := e.srv.entityService.UpdateEntityStatus(e.ctx, ent.ID, &e.owner.ID, entity.StatusPublished); err != nil {
			e.t.Fatal(err)
		}
	}
	// a second save stores version 1, which a restore can name
	name, attrs := ent.Name+" (saved)", jsontext.Value(`{"saved":true}`)
	if _, err := e.srv.entityService.UpdateEntity(e.ctx, ent.ID, &e.owner.ID, &entity.UpdateEntityInput{Name: &name, Attributes: &attrs}); err != nil {
		e.t.Fatal(err)
	}
	e.targets = append(e.targets, ent.ID.String())
	return ent
}

func (e *apEnv) state(id uuid.UUID) (name, status string, version int) {
	e.t.Helper()
	if err := e.db.Pool().QueryRow(e.ctx, `SELECT name, status, version FROM hatamoto_entities WHERE id = $1`, id).Scan(&name, &status, &version); err != nil {
		e.t.Fatal(err)
	}
	return
}

func (e *apEnv) apiKeyHeader(k *auth.APIKeyWithSecret) map[string]string {
	return map[string]string{"X-API-Key": k.PlainKey}
}

// stagedBy is the requester of the staged edit changeset of the entry, and its id.
func (e *apEnv) stagedBy(id uuid.UUID) (requester string, csID int64) {
	e.t.Helper()
	err := e.db.Pool().QueryRow(e.ctx, `SELECT c.requested_by, c.id FROM hatamoto_changesets c
		JOIN hatamoto_changeset_items i ON i.changeset_id = c.id
		WHERE c.kind = 'edit' AND c.state = 'staged' AND i.target_id = $1`, id.String()).Scan(&requester, &csID)
	if err != nil {
		e.t.Fatalf("no staged edit for %s: %v", id, err)
	}
	return
}

func (e *apEnv) denyRows(keyID string) []entAuditRow {
	e.t.Helper()
	return e.rows(`action = 'agent_policy.deny' AND actor_key_id = $2`, keyID)
}

func (e *apEnv) assertStaged(id uuid.UUID, keyID string) int64 {
	e.t.Helper()
	by, cs := e.stagedBy(id)
	if by != "api-key:"+keyID {
		e.t.Errorf("requested_by = %q, want api-key:%s", by, keyID)
	}
	r := e.one("changeset.edit_submit", "changeset:"+strconv.FormatInt(cs, 10))
	if r.KeyID == nil || r.KeyID.String() != keyID {
		e.t.Errorf("changeset.edit_submit row = %+v, want actor_key_id %s", r, keyID)
	}
	if n := len(e.rows(`action = 'agent_policy.review'`)); n != 0 {
		e.t.Errorf("%d agent_policy.review rows, want none", n)
	}
	return cs
}

func apText(res *mcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if t, ok := res.Content[0].(*mcp.TextContent); ok {
		return t.Text
	}
	return ""
}

func TestAgentPolicyReviewMCPThenCreatorApproves(t *testing.T) {
	e := apNew(t, true)
	k1, id1 := e.writeKey("k1")
	e.fake.set(id1, agentpolicy.RequireReview("a person must look"))
	ent := e.entity("mcp-review", false)
	sess := entAuditMCP(t, e.entAuditEnv, k1.PlainKey)

	out := entAuditMCPCall(t, e.entAuditEnv, sess, "entities_publish", map[string]any{"id": ent.ID.String()})
	if out["status"] != "pending_review" {
		t.Fatalf("entities_publish = %v, want pending_review", out)
	}
	if _, status, _ := e.state(ent.ID); status != "draft" {
		t.Fatalf("status = %s, want draft while the review is pending", status)
	}
	cs := e.assertStaged(ent.ID, id1)
	if n := len(e.denyRows(id1)); n != 0 {
		t.Errorf("a staged change wrote %d agent_policy.deny rows", n)
	}

	// The key's creator (an owner) may decide: the requester is the key, not the person.
	ctx := authzctx.With(e.ctx, authzsubject.ForSession(e.owner.ID.String(), "owner"))
	res, err := e.srv.reviewFlow.Approve(ctx, cs, "")
	if errors.Is(err, changeset.ErrSelfDecision) {
		t.Fatalf("the key's creator was refused as the requester: %v", err)
	}
	if err != nil || !res.Published {
		t.Fatalf("Approve = %+v, %v; want published", res, err)
	}
	if _, status, _ := e.state(ent.ID); status != "published" {
		t.Fatalf("status = %s after approval, want published", status)
	}
}

func TestAgentPolicyReviewRESTStages(t *testing.T) {
	e := apNew(t, true)
	k1, id1 := e.writeKey("k1")
	e.fake.set(id1, agentpolicy.RequireReview("a person must look"))
	ent := e.entity("rest-review", false)

	code, raw := e.do(http.MethodPatch, "/api/v1/entities/"+ent.ID.String()+"/status", []byte(`{"status":"published"}`), e.apiKeyHeader(k1))
	if code != http.StatusAccepted {
		t.Fatalf("PATCH status = %d %s, want 202", code, raw)
	}
	if _, status, _ := e.state(ent.ID); status != "draft" {
		t.Fatalf("status = %s, want draft", status)
	}
	e.assertStaged(ent.ID, id1)
}

func TestAgentPolicyDenyEveryWritePath(t *testing.T) {
	e := apNew(t, true)
	k2, id2 := e.writeKey("k2")
	const reason = "closed for the freeze"
	e.fake.set(id2, agentpolicy.Deny(reason))
	hdr := e.apiKeyHeader(k2)

	draft := e.entity("deny-publish", false)
	live := e.entity("deny-live", true)
	other := e.entity("deny-link", true)
	liveName, liveStatus, liveVersion := e.state(live.ID)

	// MCP publish.
	sess := entAuditMCP(t, e.entAuditEnv, k2.PlainKey)
	res, err := sess.CallTool(e.ctx, &mcp.CallToolParams{Name: "entities_publish", Arguments: map[string]any{"id": draft.ID.String()}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(apText(res), reason) {
		t.Fatalf("entities_publish = %q (error %v), want a refusal with %q", apText(res), res.IsError, reason)
	}
	if _, status, _ := e.state(draft.ID); status != "draft" {
		t.Errorf("a denied publish left status %s", status)
	}

	// REST status, edit and restore: 403 with the reason.
	for _, c := range []struct {
		name, method, path, body string
		id                       uuid.UUID
	}{
		{"status", http.MethodPatch, "/api/v1/entities/" + draft.ID.String() + "/status", `{"status":"published"}`, draft.ID},
		{"edit", http.MethodPut, "/api/v1/entities/" + live.ID.String(), `{"name":"changed by k2"}`, live.ID},
		{"restore", http.MethodPost, "/api/v1/entities/" + live.ID.String() + "/restore/1", ``, live.ID},
	} {
		code, raw := e.do(c.method, c.path, []byte(c.body), hdr)
		if code != http.StatusForbidden || !strings.Contains(string(raw), reason) {
			t.Errorf("%s = %d %s, want 403 with %q", c.name, code, raw, reason)
		}
	}

	// Link insert on a published source.
	body, _ := jsonx.Marshal(map[string]any{"source_entity_id": live.ID, "target_entity_id": other.ID, "anchor_text": "Note"})
	if code, raw := e.do(http.MethodPost, "/api/v1/linking/insert", body, hdr); code != http.StatusForbidden || !strings.Contains(string(raw), reason) {
		t.Errorf("link insert = %d %s, want 403 with %q", code, raw, reason)
	}

	if name, status, version := e.state(live.ID); name != liveName || status != liveStatus || version != liveVersion {
		t.Errorf("published entry changed: %s/%s/v%d, was %s/%s/v%d", name, status, version, liveName, liveStatus, liveVersion)
	}

	// One refusal row per refused write, all by the key and marked denied.
	rows := e.denyRows(id2)
	// draft: MCP publish + REST status; live: edit, restore, link insert.
	want := map[string]int{draft.ID.String(): 2, live.ID.String(): 3}
	got := map[string]int{}
	for _, r := range rows {
		got[r.Target]++
		if r.Kind != "api_key" || r.Detail["reason"] != reason {
			t.Errorf("deny row = %+v", r)
		}
	}
	for target, n := range want {
		if got[target] != n {
			t.Errorf("deny rows for %s = %d, want %d (all: %v)", target, got[target], n, got)
		}
	}
	if n := e.count(`SELECT count(*) FROM hatamoto_admin_audit WHERE id > $1 AND action = 'agent_policy.deny' AND actor_key_id = $2 AND result <> 'denied'`, e.baseline, id2); n != 0 {
		t.Errorf("%d deny rows with a result other than denied", n)
	}
	e.noSecrets()
}

func TestAgentPolicyChanges(t *testing.T) {
	e := apNew(t, true)
	k1, id1 := e.writeKey("k1")
	k2, id2 := e.writeKey("k2")
	e.fake.set(id1, agentpolicy.RequireReview("look"))
	e.fake.set(id2, agentpolicy.Deny("no"))

	staged := e.entity("changes-staged", false)
	refused := e.entity("changes-refused", false)
	viaSession := e.entity("changes-session", false)

	if code, raw := e.do(http.MethodPatch, "/api/v1/entities/"+staged.ID.String()+"/status", []byte(`{"status":"published"}`), e.apiKeyHeader(k1)); code != http.StatusAccepted {
		t.Fatalf("k1 PATCH = %d %s", code, raw)
	}
	for i := 0; i < 2; i++ {
		if code, _ := e.do(http.MethodPatch, "/api/v1/entities/"+refused.ID.String()+"/status", []byte(`{"status":"published"}`), e.apiKeyHeader(k2)); code != http.StatusForbidden {
			t.Fatalf("k2 PATCH = %d, want 403", code)
		}
	}
	// A person's own write must not show up under any key.
	if code, raw := e.do(http.MethodPatch, "/api/v1/entities/"+viaSession.ID.String()+"/status", []byte(`{"status":"published"}`), e.bearer()); code != http.StatusOK {
		t.Fatalf("session PATCH = %d %s", code, raw)
	}

	got2, err := agentpolicy.Changes(e.ctx, id2, e.start)
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 2 {
		t.Fatalf("Changes(K2) = %+v, want its two refusals", got2)
	}
	for i, c := range got2 {
		if c.Action != "agent_policy.deny" || c.Target != refused.ID.String() || c.Result != "denied" || c.Via != "api" {
			t.Errorf("Changes(K2)[%d] = %+v", i, c)
		}
		if i > 0 && (c.ID <= got2[i-1].ID || c.At.Before(got2[i-1].At)) {
			t.Errorf("Changes(K2) not oldest first: %+v", got2)
		}
	}

	got1, err := agentpolicy.Changes(e.ctx, id1, e.start)
	if err != nil {
		t.Fatal(err)
	}
	var submits int
	for _, c := range got1 {
		switch c.Action {
		case "changeset.edit_submit":
			submits++
		case "agent_policy.deny":
			t.Errorf("Changes(K1) holds a refusal: %+v", c)
		}
		if c.Target == viaSession.ID.String() || c.Target == refused.ID.String() {
			t.Errorf("Changes(K1) holds another actor's row: %+v", c)
		}
	}
	if submits != 1 {
		t.Errorf("Changes(K1) = %+v, want one changeset.edit_submit", got1)
	}
}

func TestAgentPolicyNothingRegisteredPublishesDirectly(t *testing.T) {
	e := apNew(t, false)
	k1, id1 := e.writeKey("k1")
	ent := e.entity("default", false)
	sess := entAuditMCP(t, e.entAuditEnv, k1.PlainKey)

	out := entAuditMCPCall(t, e.entAuditEnv, sess, "entities_publish", map[string]any{"id": ent.ID.String()})
	if out["status"] != "published" {
		t.Fatalf("entities_publish = %v, want published", out)
	}
	if _, status, _ := e.state(ent.ID); status != "published" {
		t.Fatalf("status = %s, want published", status)
	}
	if n := len(e.rows(`action LIKE 'agent_policy.%'`)); n != 0 {
		t.Errorf("%d agent_policy rows with nothing registered", n)
	}
	if n := len(e.rows(`action = 'changeset.edit_submit' AND actor_key_id = $2`, id1)); n != 0 {
		t.Errorf("%d review rows with nothing registered", n)
	}
}
