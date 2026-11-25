package mcpserver

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/entity"
)

func dryRunUpdate(t *testing.T, r *rig, id string, args map[string]any) (map[string]any, string, bool) {
	t.Helper()
	args["id"], args["dry_run"] = id, true
	text, isErr := r.call(t, "entities_update", args)
	if isErr {
		return nil, text, true
	}
	return decode(t, text), text, false
}

func changesOf(t *testing.T, m map[string]any) []any {
	t.Helper()
	ch, ok := m["changes"].([]any)
	if !ok {
		t.Fatalf("changes missing: %v", m)
	}
	return ch
}

func TestEntitiesUpdateDryRunStoresNothing(t *testing.T) {
	r, e := newVersionRig(t)
	m, text, isErr := dryRunUpdate(t, r, e.ID.String(), map[string]any{"attributes": map[string]any{"a": 2}})
	if isErr {
		t.Fatalf("dry run: %s", text)
	}
	want := []any{map[string]any{"path": "a", "before": float64(1), "after": float64(2)}}
	if m["dry_run"] != true || m["version"] != float64(3) || !reflect.DeepEqual(changesOf(t, m), want) {
		t.Fatalf("preview: %s", text)
	}
	if r.st.ents[e.ID].Version != 3 || string(r.st.ents[e.ID].Attributes) != `{"a":1}` || len(r.st.updates) != 0 {
		t.Errorf("dry run stored: v%d %s updates=%d", r.st.ents[e.ID].Version, r.st.ents[e.ID].Attributes, len(r.st.updates))
	}
}

func TestEntitiesUpdateDryRunVersionRoundTrip(t *testing.T) {
	r, e := newVersionRig(t)
	id := e.ID.String()
	m, text, isErr := dryRunUpdate(t, r, id, map[string]any{"attributes": map[string]any{"a": 2}})
	if isErr || m["version"] != float64(3) {
		t.Fatalf("dry run: %v %s", isErr, text)
	}
	if text, isErr := r.call(t, "entities_update", map[string]any{"id": id, "attributes": map[string]any{"a": 5}}); isErr || r.st.ents[e.ID].Version != 4 {
		t.Fatalf("real update: %v %s", isErr, text)
	}
	text, isErr = r.call(t, "entities_update", map[string]any{"id": id, "attributes": map[string]any{"a": 2}, "version": 3})
	if !isErr || !strings.HasPrefix(text, "conflict:") || !strings.Contains(text, "stale version 3; current version is 4") {
		t.Fatalf("stale apply: %v %q", isErr, text)
	}

	r2, e2 := newVersionRig(t)
	m, text, isErr = dryRunUpdate(t, r2, e2.ID.String(), map[string]any{"attributes": map[string]any{"a": 2}})
	if isErr {
		t.Fatalf("dry run: %s", text)
	}
	ch := changesOf(t, m)[0].(map[string]any)
	if text, isErr := r2.call(t, "entities_update", map[string]any{"id": e2.ID.String(), "attributes": map[string]any{"a": 2}, "version": 3}); isErr {
		t.Fatalf("apply: %s", text)
	}
	if got := storedAttrs(t, r2, e2)["a"]; got != ch["after"] {
		t.Errorf("stored a = %v, dry run said %v", got, ch["after"])
	}
}

func TestEntitiesUpdateDryRunStaleVersion(t *testing.T) {
	r, e := newVersionRig(t)
	_, text, isErr := dryRunUpdate(t, r, e.ID.String(), map[string]any{"attributes": map[string]any{"a": 2}, "version": 2})
	if !isErr || !strings.HasPrefix(text, "conflict:") || !strings.Contains(text, "stale version 2; current version is 3") {
		t.Fatalf("stale: %v %q", isErr, text)
	}
}

func TestEntitiesUpdateDryRunAudit(t *testing.T) {
	st := newMemStore()
	sc := st.schema("note", false, `{}`)
	e := st.add(sc, entity.StatusDraft, `{"a":1}`)
	e.Schema = sc
	audit := &recAudit{}
	svc := entity.NewService(nil, versionedStatusRepo{memRepo{st: st}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	scopes := []hmauth.APIKeyScope{hmauth.ScopeReadEntities, hmauth.ScopeWriteEntities}
	r := &rig{st: st, sess: session(t, newTestServer(t, scopes, Deps{Entities: svc, BuilderEntities: st, Audit: audit}))}
	if _, text, isErr := dryRunUpdate(t, r, e.ID.String(), map[string]any{"attributes": map[string]any{"a": 2}}); isErr {
		t.Fatalf("dry run: %s", text)
	}
	if len(audit.entries) != 1 || audit.entries[0].Tool != "entities_update" || !strings.Contains(string(audit.entries[0].Args), `"dry_run":true`) {
		t.Fatalf("audit entries: %+v", audit.entries)
	}
}

func TestEntitiesUpdateDryRunRateLimited(t *testing.T) {
	st := newMemStore()
	sc := st.schema("note", false, `{}`)
	e := st.add(sc, entity.StatusDraft, `{"a":1}`)
	e.Schema = sc
	svc := entity.NewService(nil, versionedStatusRepo{memRepo{st: st}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	scopes := []hmauth.APIKeyScope{hmauth.ScopeReadEntities, hmauth.ScopeWriteEntities}
	sess := session(t, newTestServer(t, scopes, Deps{Entities: svc, BuilderEntities: st, Limiter: &countLimiter{max: 12}}))
	args := map[string]any{"id": e.ID.String(), "attributes": map[string]any{"a": 2}, "dry_run": true}
	limited := false
	for range 30 {
		if _, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "entities_update", Arguments: args}); err != nil {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("dry run calls were never rate limited")
	}
	if len(st.updates) != 0 {
		t.Errorf("dry runs stored %d updates", len(st.updates))
	}
}

func TestEntitiesUpdateDryRunMerge(t *testing.T) {
	r, e := newVersionRig(t)
	id := e.ID.String()
	for _, tc := range []struct {
		attrs         map[string]any
		path          string
		before, after any
	}{
		{map[string]any{"b": 2}, "b", nil, float64(2)},
		{map[string]any{"a": nil}, "a", float64(1), nil},
	} {
		m, text, isErr := dryRunUpdate(t, r, id, map[string]any{"merge": true, "attributes": tc.attrs})
		if isErr {
			t.Fatalf("merge dry run: %s", text)
		}
		want := []any{map[string]any{"path": tc.path, "before": tc.before, "after": tc.after}}
		if got := changesOf(t, m); !reflect.DeepEqual(got, want) {
			t.Errorf("changes = %v, want %v (%s)", got, want, text)
		}
	}
	if got := string(r.st.ents[e.ID].Attributes); got != `{"a":1}` || r.st.ents[e.ID].Version != 3 {
		t.Errorf("stored %s v%d", got, r.st.ents[e.ID].Version)
	}
}

func TestEntitiesUpdateDryRunMergePageDocRules(t *testing.T) {
	st := newMemStore()
	partSc := st.schema(entity.PartSchemaSlug, true, `{}`)
	slotDoc := `{"v":2,"nodes":[{"id":"s","t":"slot"}]}`
	e := st.add(partSc, entity.StatusDraft, `{"slot":"single","page_doc":`+slotDoc+`}`)
	e.Schema = partSc
	svc := entity.NewService(nil, versionedStatusRepo{memRepo{st: st}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	scopes := []hmauth.APIKeyScope{hmauth.ScopeReadEntities, hmauth.ScopeWriteEntities}
	r := &rig{st: st, sess: session(t, newTestServer(t, scopes, Deps{Entities: svc, BuilderEntities: st}))}

	_, text, isErr := dryRunUpdate(t, r, e.ID.String(), map[string]any{"merge": true, "attributes": map[string]any{"slot": "header"}})
	if !isErr || !strings.Contains(text, "no Content slot") {
		t.Fatalf("dry run merge to header: err=%v %q", isErr, text)
	}
	if got := string(st.ents[e.ID].Attributes); !strings.Contains(got, `"single"`) {
		t.Errorf("stored %s", got)
	}
}
