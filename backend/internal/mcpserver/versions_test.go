package mcpserver

import (
	"context"
	"io"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/entity"
)

// versionsRepo serves stored versions over the versioned repo.
type versionsRepo struct {
	versionedStatusRepo
	vers []*entity.EntityVersion // newest first
}

func (r versionsRepo) GetVersion(_ context.Context, _ uuid.UUID, v int) (*entity.EntityVersion, error) {
	for _, ev := range r.vers {
		if ev.Version == v {
			return ev, nil
		}
	}
	return nil, entity.ErrVersionNotFound
}

func (r versionsRepo) ListVersionsPage(_ context.Context, _ uuid.UUID, offset, limit int) ([]*entity.EntityVersion, int, error) {
	out := []*entity.EntityVersion{}
	for i, ev := range r.vers {
		if i >= offset && len(out) < limit {
			light := *ev
			light.Attributes = nil
			out = append(out, &light)
		}
	}
	return out, len(r.vers), nil
}

// newVersionsRig serves a draft entity at version 3 with two stored versions:
// v2 by an unknown user, v1 by fakeOwner (named "First").
func newVersionsRig(t *testing.T) (*rig, *entity.Entity) {
	t.Helper()
	st := newMemStore()
	sc := st.schema("note", false, `{}`)
	e := st.add(sc, entity.StatusDraft, `{"a":1}`)
	e.Schema = sc
	stranger, owner := uuid.New(), fakeOwner
	sum2, sum1 := "second", "first"
	vers := []*entity.EntityVersion{
		{EntityID: e.ID, Version: 2, Attributes: []byte(`{"a":5}`), ChangedBy: &stranger, ChangeSummary: &sum2, CreatedAt: time.Now()},
		{EntityID: e.ID, Version: 1, Attributes: []byte(`{"a":0,"page_doc":{"v":2,"nodes":[]}}`), ChangedBy: &owner, ChangeSummary: &sum1, CreatedAt: time.Now().Add(-time.Hour)},
	}
	sort.Slice(vers, func(i, j int) bool { return vers[i].Version > vers[j].Version })
	svc := entity.NewService(nil, versionsRepo{versionedStatusRepo{memRepo{st: st}}, vers}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := &rig{st: st, audit: &recAudit{}}
	users := func(_ context.Context, id uuid.UUID) (string, bool) { return "First", id == fakeOwner }
	scopes := []hmauth.APIKeyScope{hmauth.ScopeReadEntities, hmauth.ScopeWriteEntities}
	r.sess = session(t, newTestServer(t, scopes, Deps{Entities: svc, BuilderEntities: st, Audit: r.audit, UserName: users,
		PageURL: func(context.Context, *entity.Entity) string { return "/p" }}))
	return r, e
}

func TestVersionToolsRegistered(t *testing.T) {
	r, _ := newVersionsRig(t)
	list, err := r.sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, tool := range list.Tools {
		switch tool.Name {
		case "entities_versions", "entities_version_get", "entities_restore":
			found[tool.Name] = true
			destructive := tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint
			if destructive != (tool.Name == "entities_restore") {
				t.Errorf("%s destructiveHint = %v", tool.Name, destructive)
			}
		}
	}
	if len(found) != 3 {
		t.Fatalf("registered = %v", found)
	}
}

func TestEntitiesVersionsPaging(t *testing.T) {
	r, e := newVersionsRig(t)
	id := e.ID.String()

	text, isErr := r.call(t, "entities_versions", map[string]any{"id": id})
	if isErr {
		t.Fatal(text)
	}
	m := decode(t, text)
	rows := m["versions"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["version"] != float64(2) || rows[1].(map[string]any)["version"] != float64(1) {
		t.Fatalf("rows = %s", text)
	}
	if m["current_version"] != float64(3) || m["total"] != float64(2) || m["page"] != float64(1) || m["limit"] != float64(20) || m["total_pages"] != float64(1) {
		t.Errorf("answer = %s", text)
	}
	if rows[0].(map[string]any)["changed_by"] != "Unknown user" || rows[1].(map[string]any)["changed_by"] != "First" {
		t.Errorf("changed_by = %s", text)
	}
	if strings.Contains(text, "attributes") {
		t.Errorf("list carries attributes: %s", text)
	}

	text, _ = r.call(t, "entities_versions", map[string]any{"id": id, "limit": 1, "page": 2})
	m = decode(t, text)
	if rows := m["versions"].([]any); len(rows) != 1 || rows[0].(map[string]any)["version"] != float64(1) || m["total_pages"] != float64(2) {
		t.Errorf("page 2 = %s", text)
	}

	text, _ = r.call(t, "entities_versions", map[string]any{"id": id, "limit": 500})
	if m = decode(t, text); m["limit"] != float64(100) {
		t.Errorf("limit 500 reported %v", m["limit"])
	}

	for arg := range map[string]bool{"limit": true, "page": true} {
		text, isErr = r.call(t, "entities_versions", map[string]any{"id": id, arg: 0})
		if !isErr || !strings.Contains(text, arg) || strings.Contains(text, "internal error") {
			t.Errorf("%s 0: err=%v %q", arg, isErr, text)
		}
	}
}

func TestEntitiesVersionGet(t *testing.T) {
	r, e := newVersionsRig(t)
	id := e.ID.String()

	text, isErr := r.call(t, "entities_version_get", map[string]any{"id": id, "version": 1})
	if isErr {
		t.Fatal(text)
	}
	m := decode(t, text)
	attrs, _ := m["attributes"].(map[string]any)
	if attrs["a"] != float64(0) || attrs["page_doc"] == nil || m["changed_by"] != "First" || m["change_summary"] != "first" {
		t.Errorf("version = %s", text)
	}

	text, isErr = r.call(t, "entities_version_get", map[string]any{"id": id, "version": 9})
	if !isErr || strings.Contains(text, "internal error") {
		t.Errorf("missing version: err=%v %q", isErr, text)
	}
	text, isErr = r.call(t, "entities_version_get", map[string]any{"id": id, "version": 0})
	if !isErr || !strings.Contains(text, "version") {
		t.Errorf("version 0: err=%v %q", isErr, text)
	}
}

func TestEntitiesRestoreVersion(t *testing.T) {
	r, e := newVersionsRig(t)
	id := e.ID.String()

	text, isErr := r.call(t, "entities_restore", map[string]any{"id": id, "version": 1, "expected_version": 2})
	if !isErr || !strings.HasPrefix(text, "conflict:") || !strings.Contains(text, "stale version 2; current version is 3") {
		t.Fatalf("stale: err=%v %q", isErr, text)
	}
	if len(r.st.updates) != 0 || r.st.ents[e.ID].Version != 3 {
		t.Fatalf("stale restore stored: %d updates, v%d", len(r.st.updates), r.st.ents[e.ID].Version)
	}

	text, isErr = r.call(t, "entities_restore", map[string]any{"id": id, "version": 1, "expected_version": 3})
	if isErr {
		t.Fatal(text)
	}
	last := r.st.updates[len(r.st.updates)-1]
	if last.ChangeSummary == nil || *last.ChangeSummary != "[mcp:t] Restored from version 1" {
		t.Errorf("summary = %v", last.ChangeSummary)
	}
	if got := decode(t, text); got["id"] != id {
		t.Errorf("answer = %s", text)
	}
	if !strings.Contains(string(r.st.ents[e.ID].Attributes), `"a":0`) {
		t.Errorf("attributes = %s", r.st.ents[e.ID].Attributes)
	}

	text, isErr = r.call(t, "entities_restore", map[string]any{"id": id, "version": 9})
	if !isErr || strings.Contains(text, "internal error") {
		t.Errorf("missing version: err=%v %q", isErr, text)
	}
}

// A race with no expected_version is still a conflict, not an internal error.
func TestEntitiesRestoreRaceIsConflict(t *testing.T) {
	st := newMemStore()
	sc := st.schema("note", false, `{}`)
	e := st.add(sc, entity.StatusDraft, `{"a":1}`)
	e.Schema = sc
	raceRepo := &racingRepo{versionsRepo: versionsRepo{versionedStatusRepo{memRepo{st: st}}, []*entity.EntityVersion{{EntityID: e.ID, Version: 1, Attributes: []byte(`{"a":0}`)}}}}
	svc := entity.NewService(nil, raceRepo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	sess := session(t, newTestServer(t, []hmauth.APIKeyScope{hmauth.ScopeReadEntities, hmauth.ScopeWriteEntities}, Deps{Entities: svc, BuilderEntities: st,
		PageURL: func(context.Context, *entity.Entity) string { return "/p" }}))
	text, isErr := call(t, sess, "entities_restore", map[string]any{"id": e.ID.String(), "version": 1})
	if !isErr || !strings.HasPrefix(text, "conflict:") {
		t.Fatalf("race: err=%v %q", isErr, text)
	}
}

// racingRepo loses every update to a concurrent edit.
type racingRepo struct{ versionsRepo }

func (r *racingRepo) Update(context.Context, uuid.UUID, *uuid.UUID, *entity.UpdateEntityInput) (*entity.Entity, error) {
	return nil, &entity.VersionConflictError{Current: 4}
}

func TestVersionToolsAudit(t *testing.T) {
	r, e := newVersionsRig(t)
	id := e.ID.String()
	r.call(t, "entities_versions", map[string]any{"id": id})
	r.call(t, "entities_version_get", map[string]any{"id": id, "version": 9}) // fails
	r.call(t, "entities_restore", map[string]any{"id": id, "version": 1})
	want := []string{"entities_versions", "entities_version_get", "entities_restore"}
	if len(r.audit.entries) != len(want) {
		t.Fatalf("audit rows = %d, want %d: %+v", len(r.audit.entries), len(want), r.audit.entries)
	}
	for i, ent := range r.audit.entries {
		if ent.Tool != want[i] || ent.KeyName != "t" {
			t.Errorf("row %d = %s/%s", i, ent.Tool, ent.KeyName)
		}
	}
}
