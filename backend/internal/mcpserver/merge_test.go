package mcpserver

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

// storedAttrs decodes the attributes the rig's store holds for e.
func storedAttrs(t *testing.T, r *rig, e *entity.Entity) map[string]any {
	t.Helper()
	var got map[string]any
	if err := jsonx.Unmarshal(r.st.ents[e.ID].Attributes, &got); err != nil {
		t.Fatalf("stored attributes: %v", err)
	}
	return got
}

func TestEntitiesUpdateMerge(t *testing.T) {
	r, e := newVersionRig(t)
	id := e.ID.String()

	text, isErr := r.call(t, "entities_update", map[string]any{"id": id, "merge": true, "attributes": map[string]any{"b": 2}, "version": 3})
	if isErr || r.st.ents[e.ID].Version != 4 {
		t.Fatalf("merge: err=%v v%d %q", isErr, r.st.ents[e.ID].Version, text)
	}
	if got := storedAttrs(t, r, e); len(got) != 2 || got["a"] != float64(1) || got["b"] != float64(2) {
		t.Errorf("merged attributes: %v", got)
	}

	text, isErr = r.call(t, "entities_update", map[string]any{"id": id, "merge": true, "attributes": map[string]any{"a": nil}})
	if isErr {
		t.Fatalf("null: %q", text)
	}
	if got := storedAttrs(t, r, e); len(got) != 1 || got["b"] != float64(2) {
		t.Errorf("null should remove a: %v", got)
	}
}

func TestEntitiesUpdateReplaceWithoutMerge(t *testing.T) {
	r, e := newVersionRig(t)
	text, isErr := r.call(t, "entities_update", map[string]any{"id": e.ID.String(), "attributes": map[string]any{"c": 3}})
	if isErr {
		t.Fatalf("replace: %q", text)
	}
	if got := string(r.st.ents[e.ID].Attributes); got != `{"c":3}` {
		t.Errorf("replace stored %s", got)
	}
}

func TestEntitiesUpdateMergeStaleVersion(t *testing.T) {
	r, e := newVersionRig(t)
	text, isErr := r.call(t, "entities_update", map[string]any{"id": e.ID.String(), "merge": true, "attributes": map[string]any{"b": 2}, "version": 2})
	if !isErr || !strings.HasPrefix(text, "conflict:") || !strings.Contains(text, "stale version 2; current version is") {
		t.Fatalf("stale: err=%v %q", isErr, text)
	}
	if got := string(r.st.ents[e.ID].Attributes); got != `{"a":1}` || r.st.ents[e.ID].Version != 3 {
		t.Errorf("stale merge stored: %s v%d", got, r.st.ents[e.ID].Version)
	}
}

func TestEntitiesUpdateMergeValidatesResult(t *testing.T) {
	st := newMemStore()
	sc := st.schema("note", false, `{"type":"object","required":["title"],"properties":{"title":{"type":"string"}}}`)
	e := st.add(sc, entity.StatusDraft, `{"title":"T","x":1}`)
	e.Schema = sc
	svc := entity.NewService(nil, versionedStatusRepo{memRepo{st: st}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	scopes := []hmauth.APIKeyScope{hmauth.ScopeReadEntities, hmauth.ScopeWriteEntities}
	r := &rig{st: st, sess: session(t, newTestServer(t, scopes, Deps{Entities: svc, BuilderEntities: st, PageURL: nil}))}

	text, isErr := r.call(t, "entities_update", map[string]any{"id": e.ID.String(), "merge": true, "attributes": map[string]any{"title": nil}})
	if !isErr || !strings.Contains(text, "title") {
		t.Fatalf("removing a required key: err=%v %q", isErr, text)
	}
	if got := string(st.ents[e.ID].Attributes); got != `{"title":"T","x":1}` {
		t.Errorf("refused merge stored %s", got)
	}
}

// The page_doc rules judge the merged attributes: the fragment alone carries
// no page_doc and would pass.
func TestEntitiesUpdateMergeChecksPageDocRules(t *testing.T) {
	st := newMemStore()
	partSc := st.schema(entity.PartSchemaSlug, true, `{}`)
	slotDoc := `{"v":2,"nodes":[{"id":"s","t":"slot"}]}`
	e := st.add(partSc, entity.StatusDraft, `{"slot":"single","page_doc":`+slotDoc+`}`)
	e.Schema = partSc
	svc := entity.NewService(nil, versionedStatusRepo{memRepo{st: st}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	scopes := []hmauth.APIKeyScope{hmauth.ScopeReadEntities, hmauth.ScopeWriteEntities}
	r := &rig{st: st, sess: session(t, newTestServer(t, scopes, Deps{Entities: svc, BuilderEntities: st}))}

	text, isErr := r.call(t, "entities_update", map[string]any{"id": e.ID.String(), "merge": true, "attributes": map[string]any{"slot": "header"}})
	if !isErr || !strings.Contains(text, "no Content slot") {
		t.Fatalf("merge to header: err=%v %q", isErr, text)
	}
	if got := string(st.ents[e.ID].Attributes); !strings.Contains(got, `"single"`) {
		t.Errorf("refused merge stored %s", got)
	}
}
