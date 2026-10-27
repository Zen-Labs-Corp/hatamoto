package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/entity"
)

// memStore is an in-memory entity.BuilderStore: UpdateEntity bumps the version
// only when the attributes change, as the real service does.
type memStore struct {
	mu        sync.Mutex
	ents      map[uuid.UUID]*entity.Entity
	schemas   map[uuid.UUID]*entity.EntitySchema
	updates   []entity.UpdateEntityInput
	statusLog []entity.EntityStatus
	statusErr error // UpdateEntityStatus fails with it
}

func newMemStore() *memStore {
	return &memStore{ents: map[uuid.UUID]*entity.Entity{}, schemas: map[uuid.UUID]*entity.EntitySchema{}}
}

func (m *memStore) schema(slug string, builtin bool, schemaJSON string) *entity.EntitySchema {
	sc := &entity.EntitySchema{ID: uuid.New(), Slug: slug, Name: slug, IsBuiltin: builtin, Schema: json.RawMessage(schemaJSON)}
	m.schemas[sc.ID] = sc
	return sc
}

func (m *memStore) add(sc *entity.EntitySchema, status entity.EntityStatus, attrs string) *entity.Entity {
	e := &entity.Entity{ID: uuid.New(), SchemaID: sc.ID, Name: "E", Slug: "e-" + uuid.NewString()[:6], Status: status, Version: 3, Attributes: json.RawMessage(attrs)}
	m.ents[e.ID] = e
	return e
}

func (m *memStore) GetEntity(_ context.Context, id uuid.UUID) (*entity.Entity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.ents[id]
	if !ok {
		return nil, entity.ErrEntityNotFound
	}
	c := *e
	return &c, nil
}

func (m *memStore) GetSchema(_ context.Context, id uuid.UUID) (*entity.EntitySchema, error) {
	if sc, ok := m.schemas[id]; ok {
		return sc, nil
	}
	return nil, entity.ErrSchemaNotFound
}

func (m *memStore) UpdateEntity(_ context.Context, id uuid.UUID, _ *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updates = append(m.updates, *in)
	e := m.ents[id]
	if in.ExpectedVersion != nil && *in.ExpectedVersion != e.Version {
		return nil, &entity.VersionConflictError{Current: e.Version}
	}
	var a, b any
	_ = json.Unmarshal(e.Attributes, &a)
	_ = json.Unmarshal(*in.Attributes, &b)
	if !reflect.DeepEqual(a, b) {
		e.Attributes = *in.Attributes
		e.Version++
	}
	c := *e
	return &c, nil
}

func (m *memStore) UpdateEntityStatus(_ context.Context, id uuid.UUID, _ *uuid.UUID, st entity.EntityStatus) (*entity.Entity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statusLog = append(m.statusLog, st)
	if m.statusErr != nil {
		return nil, m.statusErr
	}
	m.ents[id].Status = st
	c := *m.ents[id]
	return &c, nil
}

type recAudit struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func (r *recAudit) Record(_ context.Context, e AuditEntry) {
	r.mu.Lock()
	r.entries = append(r.entries, e)
	r.mu.Unlock()
}

type rig struct {
	st    *memStore
	page  *entity.Entity
	sess  *mcp.ClientSession
	audit *recAudit
}

const heroDoc = `{"v":2,"nodes":[{"id":"h","t":"heading","p":{"text":"Hi","level":1}}]}`

func newRig(t *testing.T, scopes ...hmauth.APIKeyScope) *rig {
	t.Helper()
	if len(scopes) == 0 {
		scopes = []hmauth.APIKeyScope{hmauth.ScopeReadEntities, hmauth.ScopeWriteEntities}
	}
	st := newMemStore()
	pageSc := st.schema("page", true, `{}`)
	r := &rig{st: st, audit: &recAudit{}}
	r.page = st.add(pageSc, entity.StatusDraft, `{"page_doc":`+heroDoc+`}`)
	r.sess = session(t, newTestServer(t, scopes, Deps{BuilderEntities: st, Audit: r.audit}))
	return r
}

func (r *rig) call(t *testing.T, tool string, args map[string]any) (string, bool) {
	t.Helper()
	return call(t, r.sess, tool, args)
}

func (r *rig) apply(t *testing.T, version int, ops ...map[string]any) (string, bool) {
	t.Helper()
	return r.call(t, "builder_apply_ops", map[string]any{"entity_id": r.page.ID.String(), "version": version, "ops": ops})
}

func decode(t *testing.T, text string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("%v in %q", err, text)
	}
	return m
}

func TestBuilderGet(t *testing.T) {
	r := newRig(t)
	text, isErr := r.call(t, "builder_get", map[string]any{"entity_id": r.page.ID.String()})
	if isErr {
		t.Fatal(text)
	}
	m := decode(t, text)
	if m["version"] != float64(3) || m["schema"] != "page" || m["role"] != "page" {
		t.Errorf("get: %s", text)
	}
	doc := m["doc"].(map[string]any)
	if len(doc["nodes"].([]any)) != 1 {
		t.Errorf("doc: %v", doc)
	}

	// A page with no layout yet starts empty.
	fresh := r.st.add(r.st.schemas[r.page.SchemaID], entity.StatusDraft, `{}`)
	text, isErr = r.call(t, "builder_get", map[string]any{"entity_id": fresh.ID.String()})
	if isErr || !strings.Contains(text, `"nodes":[]`) {
		t.Errorf("empty page: err=%v %s", isErr, text)
	}
}

func TestBuilderToolsRefuseNonBuilderEntities(t *testing.T) {
	r := newRig(t)
	post := r.st.add(r.st.schema("post", true, `{}`), entity.StatusDraft, `{"body":"x"}`)
	person := r.st.add(r.st.schema("person", true, `{}`), entity.StatusDraft, `{}`)
	legacy := r.st.add(r.st.schemas[r.page.SchemaID], entity.StatusDraft, `{"page_sections":[{"id":"s1","type":"hero","content":{}}]}`)
	for name, e := range map[string]*entity.Entity{"post": post, "person": person} {
		for _, tool := range []string{"builder_get", "builder_validate"} {
			text, isErr := r.call(t, tool, map[string]any{"entity_id": e.ID.String()})
			if !isErr || !strings.HasPrefix(text, "not_builder:") {
				t.Errorf("%s %s: err=%v %q", tool, name, isErr, text)
			}
		}
		text, isErr := r.call(t, "builder_apply_ops", map[string]any{"entity_id": e.ID.String(), "version": 3, "ops": []map[string]any{{"op": "remove", "id": "x"}}})
		if !isErr || !strings.HasPrefix(text, "not_builder:") {
			t.Errorf("apply %s: err=%v %q", name, isErr, text)
		}
	}
	if text, isErr := r.call(t, "builder_get", map[string]any{"entity_id": legacy.ID.String()}); !isErr || !strings.Contains(text, "not_builder: legacy") {
		t.Errorf("legacy: err=%v %q", isErr, text)
	}
	if text, isErr := r.call(t, "builder_get", map[string]any{"entity_id": uuid.NewString()}); !isErr || !strings.Contains(text, "not found") {
		t.Errorf("unknown: err=%v %q", isErr, text)
	}
	if len(r.st.updates) != 0 {
		t.Errorf("a refused call wrote %d times", len(r.st.updates))
	}
}

func TestBuilderApplyOps(t *testing.T) {
	r := newRig(t)
	text, isErr := r.apply(t, 3,
		map[string]any{"op": "insert", "node": map[string]any{"t": "container", "p": map[string]any{"tag": "section"}, "c": []any{map[string]any{"t": "text", "p": map[string]any{"text": "left"}}}}},
		map[string]any{"op": "setProp", "id": "h", "key": "text", "value": "Hello"},
	)
	if isErr {
		t.Fatal(text)
	}
	m := decode(t, text)
	if m["version"] != float64(4) || m["changed"] != true || m["published"] != false {
		t.Errorf("reply: %s", text)
	}
	if ids := m["ids"].([]any); len(ids) != 2 {
		t.Errorf("ids: %v", ids)
	}
	if s := m["doc_summary"].(map[string]any); s["nodes"] != float64(3) || s["top_level"] != float64(2) {
		t.Errorf("summary: %v", s)
	}
	if len(r.st.updates) != 1 || *r.st.updates[0].ChangeSummary != "[mcp:t] Edited by t via MCP" {
		t.Errorf("updates: %+v", r.st.updates)
	}
	// The stored doc has the edits; builder_get shows the new version.
	got, _ := r.call(t, "builder_get", map[string]any{"entity_id": r.page.ID.String()})
	if !strings.Contains(got, `"Hello"`) || decode(t, got)["version"] != float64(4) {
		t.Errorf("get after apply: %s", got)
	}
}

func TestBuilderApplyOpsConflict(t *testing.T) {
	r := newRig(t)
	if text, isErr := r.apply(t, 3, map[string]any{"op": "setProp", "id": "h", "key": "text", "value": "A"}); isErr {
		t.Fatal(text)
	}
	text, isErr := r.apply(t, 3, map[string]any{"op": "setProp", "id": "h", "key": "text", "value": "B"})
	if !isErr || !strings.HasPrefix(text, "conflict:") || !strings.Contains(text, "current version is 4") {
		t.Fatalf("stale: err=%v %q", isErr, text)
	}
	if len(r.st.updates) != 1 {
		t.Errorf("the stale call wrote: %d updates", len(r.st.updates))
	}
}

func TestBuilderApplyOpsInvalidAbortsBatch(t *testing.T) {
	r := newRig(t)
	text, isErr := r.apply(t, 3,
		map[string]any{"op": "setProp", "id": "h", "key": "text", "value": "Changed"},
		map[string]any{"op": "remove", "id": "nope"},
	)
	if !isErr || !strings.HasPrefix(text, "invalid_page_doc: op 1 (remove)") {
		t.Fatalf("err=%v %q", isErr, text)
	}
	if len(r.st.updates) != 0 {
		t.Errorf("an invalid batch stored %d times", len(r.st.updates))
	}
}

func TestBuilderApplyOpsSharedRules(t *testing.T) {
	r := newRig(t)
	// A slot belongs in a layout part only.
	text, isErr := r.apply(t, 3, map[string]any{"op": "insert", "node": map[string]any{"t": "slot"}})
	if !isErr || !strings.Contains(text, "part_slot") {
		t.Errorf("slot on a page: err=%v %q", isErr, text)
	}
	// A block may not embed itself.
	block := r.st.add(r.st.schema("block", true, `{}`), entity.StatusDraft, `{"page_doc":{"v":2,"nodes":[]}}`)
	text, isErr = call(t, r.sess, "builder_apply_ops", map[string]any{"entity_id": block.ID.String(), "version": 3,
		"ops": []map[string]any{{"op": "insert", "node": map[string]any{"t": "ref", "p": map[string]any{"block": block.ID.String()}}}}})
	if !isErr || !strings.Contains(text, "ref_cycle") {
		t.Errorf("ref cycle: err=%v %q", isErr, text)
	}
	// ...and may not be published.
	text, isErr = call(t, r.sess, "builder_apply_ops", map[string]any{"entity_id": block.ID.String(), "version": 3, "publish": true,
		"ops": []map[string]any{{"op": "insert", "node": map[string]any{"t": "text", "p": map[string]any{"text": "x"}}}}})
	if !isErr || !strings.Contains(text, "never published") {
		t.Errorf("publish a block: err=%v %q", isErr, text)
	}
	if len(r.st.updates) != 0 {
		t.Errorf("refusals wrote %d times", len(r.st.updates))
	}
}

func TestBuilderApplyOpsPublish(t *testing.T) {
	r := newRig(t)
	text, isErr := call(t, r.sess, "builder_apply_ops", map[string]any{"entity_id": r.page.ID.String(), "version": 3, "publish": true,
		"ops": []map[string]any{{"op": "setProp", "id": "h", "key": "text", "value": "Live"}}})
	if isErr {
		t.Fatal(text)
	}
	if m := decode(t, text); m["published"] != true || m["status"] != "published" {
		t.Errorf("reply: %s", text)
	}
	if len(r.st.statusLog) != 1 || r.st.statusLog[0] != entity.StatusPublished {
		t.Errorf("status log: %v", r.st.statusLog)
	}
}

// A publish that fails after the save stored the edit says so (success with
// published:false and a note), so a retry at the old version is not invited.
func TestBuilderApplyOpsPublishFailsAfterSave(t *testing.T) {
	r := newRig(t)
	r.st.statusErr = entity.ErrInvalidStatus
	text, isErr := call(t, r.sess, "builder_apply_ops", map[string]any{"entity_id": r.page.ID.String(), "version": 3, "publish": true,
		"ops": []map[string]any{{"op": "setProp", "id": "h", "key": "text", "value": "Live"}}})
	if isErr {
		t.Fatalf("a stored edit must not come back as an error: %s", text)
	}
	m := decode(t, text)
	note, _ := m["note"].(string)
	if m["published"] != false || m["version"] != float64(4) || !strings.Contains(note, "Saved as version 4") || !strings.Contains(note, "could not be published") {
		t.Errorf("reply: %s", text)
	}
}

func TestBuilderApplyOpsNoOpKeepsVersion(t *testing.T) {
	r := newRig(t)
	text, isErr := r.apply(t, 3, map[string]any{"op": "setProp", "id": "h", "key": "text", "value": "Hi"})
	if isErr {
		t.Fatal(text)
	}
	if m := decode(t, text); m["version"] != float64(3) || m["changed"] != false {
		t.Errorf("no-op reply: %s", text)
	}
}

func TestBuilderApplyOpsNeedsWriteScope(t *testing.T) {
	r := newRig(t, hmauth.ScopeReadEntities)
	text, isErr := r.apply(t, 3, map[string]any{"op": "remove", "id": "h"})
	if !isErr || !strings.Contains(text, "forbidden") || !strings.Contains(text, "write:entities") {
		t.Errorf("read-only key: err=%v %q", isErr, text)
	}
	if text, isErr := r.call(t, "builder_get", map[string]any{"entity_id": r.page.ID.String()}); isErr {
		t.Errorf("a read-only key may read: %q", text)
	}
}

func TestBuilderApplyOpsLimits(t *testing.T) {
	r := newRig(t)
	many := make([]map[string]any, maxOps+1)
	for i := range many {
		many[i] = map[string]any{"op": "setProp", "id": "h", "key": "text", "value": "x"}
	}
	if text, isErr := r.apply(t, 3, many...); !isErr || !strings.HasPrefix(text, "limit:") {
		t.Errorf("501 ops: err=%v %.80q", isErr, text)
	}
	text, isErr := r.apply(t, 3, map[string]any{"op": "setProp", "id": "h", "key": "text", "value": strings.Repeat("x", maxOpsBytes)})
	if !isErr || !strings.HasPrefix(text, "limit:") {
		t.Errorf("1 MiB of ops: err=%v %.80q", isErr, text)
	}
	if len(r.st.updates) != 0 {
		t.Error("a refused call wrote")
	}
}

// A large call is audited, but never whole.
func TestBuilderApplyOpsAuditArgsStaySmall(t *testing.T) {
	r := newRig(t)
	ops := make([]map[string]any, 200)
	for i := range ops {
		ops[i] = map[string]any{"op": "insert", "node": map[string]any{"t": "text", "p": map[string]any{"text": strings.Repeat("long text ", 50)}}}
	}
	if text, isErr := r.apply(t, 3, ops...); isErr {
		t.Fatal(text)
	}
	var found bool
	for _, e := range r.audit.entries {
		if e.Tool == "builder_apply_ops" {
			found = true
			if len(e.Args) > maxAuditArgs+64 {
				t.Errorf("audit args are %d bytes", len(e.Args))
			}
		}
	}
	if !found {
		t.Error("no audit entry for builder_apply_ops")
	}
}

func TestBuilderValidateStoresNothing(t *testing.T) {
	r := newRig(t)
	id := r.page.ID.String()

	text, isErr := r.call(t, "builder_validate", map[string]any{"entity_id": id})
	if m := decode(t, text); isErr || m["ok"] != true {
		t.Errorf("stored doc: %s", text)
	}
	text, _ = r.call(t, "builder_validate", map[string]any{"entity_id": id, "ops": []map[string]any{{"op": "insert", "node": map[string]any{"t": "text", "p": map[string]any{"text": "x"}}}}})
	if m := decode(t, text); m["ok"] != true || m["doc_summary"].(map[string]any)["nodes"] != float64(2) {
		t.Errorf("ops dry run: %s", text)
	}
	if gen, _ := decode(t, text)["ids"].([]any); len(gen) != 1 || gen[0].(map[string]any)["id"] == "" {
		t.Errorf("generated ids: %s", text)
	}
	text, _ = r.call(t, "builder_validate", map[string]any{"entity_id": id, "ops": []map[string]any{{"op": "remove", "id": "zz"}}})
	if m := decode(t, text); m["ok"] != false || !strings.Contains(text, "no such node") {
		t.Errorf("bad op: %s", text)
	}
	text, _ = r.call(t, "builder_validate", map[string]any{"entity_id": id, "ops": []map[string]any{{"op": "insert", "node": map[string]any{"t": "slot"}}}})
	if m := decode(t, text); m["ok"] != false || !strings.Contains(text, "part_slot") {
		t.Errorf("slot: %s", text)
	}
	text, _ = r.call(t, "builder_validate", map[string]any{"doc": map[string]any{"v": 2, "nodes": []any{map[string]any{"id": "a", "t": "nope"}}}})
	if m := decode(t, text); m["ok"] != false {
		t.Errorf("bad doc without entity: %s", text)
	}
	text, _ = r.call(t, "builder_validate", map[string]any{"doc": map[string]any{"v": 2, "nodes": []any{}}})
	if m := decode(t, text); m["ok"] != true {
		t.Errorf("good doc without entity: %s", text)
	}
	if text, isErr := r.call(t, "builder_validate", map[string]any{}); !isErr {
		t.Errorf("nothing to validate: %q", text)
	}
	if len(r.st.updates) != 0 {
		t.Errorf("validate stored %d times", len(r.st.updates))
	}
}

// The agent-facing schema of ops is an array of objects, not an opaque blob.
func TestBuilderApplyOpsSchema(t *testing.T) {
	r := newRig(t)
	res, err := r.sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != "builder_apply_ops" {
			continue
		}
		raw, _ := json.Marshal(tool.InputSchema)
		var sc struct {
			Required   []string `json:"required"`
			Properties map[string]struct {
				Type  any `json:"type"` // ["null","array"]: a nil slice is allowed
				Items struct {
					Type       string                     `json:"type"`
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"items"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &sc); err != nil {
			t.Fatal(err)
		}
		ops := sc.Properties["ops"]
		if !strings.Contains(fmt.Sprint(ops.Type), "array") || ops.Items.Type != "object" || ops.Items.Properties["op"] == nil || ops.Items.Properties["node"] == nil || ops.Items.Properties["value"] == nil {
			t.Errorf("ops schema: %s", raw)
		}
		if !slices.Contains(sc.Required, "version") || !slices.Contains(sc.Required, "entity_id") || !slices.Contains(sc.Required, "ops") {
			t.Errorf("required: %v", sc.Required)
		}
		return
	}
	t.Fatal("builder_apply_ops is not registered")
}

// entities_update used to take any page_doc whole; it now meets the builder's
// slot and block-reference rules, like the admin save. (The allowed path runs
// the real entity service; the integration session test covers it.)
func TestEntitiesUpdateRefusesSlotAndRefCycle(t *testing.T) {
	r := newRig(t)
	block := r.st.add(r.st.schema("block", true, `{}`), entity.StatusDraft, `{"page_doc":{"v":2,"nodes":[]}}`)
	update := func(id uuid.UUID, doc map[string]any) (string, bool) {
		return r.call(t, "entities_update", map[string]any{"id": id.String(), "attributes": map[string]any{"page_doc": doc}})
	}
	text, isErr := update(r.page.ID, map[string]any{"v": 2, "nodes": []any{map[string]any{"id": "s", "t": "slot"}}})
	if !isErr || !strings.HasPrefix(text, "invalid page_doc:") || !strings.Contains(text, "Content slot") {
		t.Errorf("slot on a page: err=%v %q", isErr, text)
	}
	text, isErr = update(block.ID, map[string]any{"v": 2, "nodes": []any{map[string]any{"id": "r", "t": "ref", "p": map[string]any{"block": block.ID.String()}}}})
	if !isErr || !strings.HasPrefix(text, "invalid page_doc:") {
		t.Errorf("ref cycle: err=%v %q", isErr, text)
	}
	if len(r.st.updates) != 0 {
		t.Errorf("a refused update wrote %d times", len(r.st.updates))
	}
}

// The part slot rules read the slot being stored (entities_update sends all
// attributes), not the stored one.
func TestCheckPageDocRulesReadsIncomingSlot(t *testing.T) {
	r := newRig(t)
	s := &Server{d: Deps{BuilderEntities: r.st}}
	partSc := r.st.schema(entity.PartSchemaSlug, true, `{}`)
	slotDoc := map[string]any{"v": 2, "nodes": []any{map[string]any{"id": "s", "t": "slot"}}}
	check := func(stored *entity.Entity, attrs map[string]any) error {
		return s.checkPageDocRules(context.Background(), stored, entity.EntityRole(context.Background(), r.st, stored), attrs)
	}
	asJSON := func(v any) string { b, _ := json.Marshal(v); return string(b) }

	single := r.st.add(partSc, entity.StatusDraft, `{"slot":"single","page_doc":`+asJSON(slotDoc)+`}`)
	if err := check(single, map[string]any{"slot": "header", "page_doc": slotDoc}); err == nil || !strings.Contains(err.Error(), "no Content slot") {
		t.Errorf("single to header with a slot node: %v", err)
	}
	header := r.st.add(partSc, entity.StatusDraft, `{"slot":"header","page_doc":{"v":2,"nodes":[]}}`)
	if err := check(header, map[string]any{"slot": "single", "page_doc": slotDoc}); err != nil {
		t.Errorf("header to single adding the slot node: %v", err)
	}
	if err := check(header, map[string]any{"slot": "header", "page_doc": slotDoc}); err == nil {
		t.Error("a slot node on a header part was accepted")
	}

	// Neither page_doc nor slot changes: a doc that broke a rule earlier does
	// not block an unrelated attribute.
	broken := r.st.add(partSc, entity.StatusDraft, `{"slot":"header","note":"a","page_doc":`+asJSON(slotDoc)+`}`)
	if err := check(broken, map[string]any{"slot": "header", "note": "b", "page_doc": slotDoc}); err != nil {
		t.Errorf("unrelated update refused: %v", err)
	}

	// Create applies the same rules, with the role of the schema.
	if err := s.checkPageDocRules(context.Background(), nil, entity.RolePage, map[string]any{"page_doc": slotDoc}); err == nil {
		t.Error("create of a page with a slot node was accepted")
	}
	if err := s.checkPageDocRules(context.Background(), nil, entity.RolePart, map[string]any{"slot": "single", "page_doc": slotDoc}); err != nil {
		t.Errorf("create of a single part: %v", err)
	}
}

// Two applies at the same version: the store compares the version under its
// own lock, so exactly one wins however the calls interleave.
func TestBuilderApplyOpsConcurrentSameVersion(t *testing.T) {
	r := newRig(t)
	version := 3
	for round := 0; round < 20; round++ {
		var wg sync.WaitGroup
		results := make([]string, 2)
		errs := make([]bool, 2)
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i], errs[i] = r.apply(t, version, map[string]any{"op": "setProp", "id": "h", "key": "text", "value": fmt.Sprintf("r%d-%d", round, i)})
			}()
		}
		wg.Wait()
		ok, conflicts := 0, 0
		for i := range results {
			switch {
			case !errs[i]:
				ok++
			case strings.HasPrefix(results[i], "conflict:"):
				conflicts++
			}
		}
		if ok != 1 || conflicts != 1 {
			t.Fatalf("round %d: %d applied, %d conflicts: %q", round, ok, conflicts, results)
		}
		version++
	}
}
