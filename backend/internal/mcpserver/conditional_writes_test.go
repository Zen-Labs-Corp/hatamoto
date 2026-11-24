package mcpserver

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/entity"
)

// versionedStatusRepo adds a status write that honours an expected version, as the
// Postgres repository does.
type versionedStatusRepo struct{ memRepo }

func (r versionedStatusRepo) UpdateStatusIf(_ context.Context, id uuid.UUID, _ *uuid.UUID, st entity.EntityStatus, expected *int) (*entity.Entity, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	e := r.st.ents[id]
	if expected != nil && *expected != e.Version {
		return nil, &entity.VersionConflictError{Current: e.Version}
	}
	e.Status = st
	c := *e
	return &c, nil
}

func (r versionedStatusRepo) UpdateStatus(ctx context.Context, id uuid.UUID, by *uuid.UUID, st entity.EntityStatus) (*entity.Entity, error) {
	return r.UpdateStatusIf(ctx, id, by, st, nil)
}

// newVersionRig serves a draft entity at version 3 through the real entity service.
func newVersionRig(t *testing.T) (*rig, *entity.Entity) {
	t.Helper()
	st := newMemStore()
	sc := st.schema("note", false, `{}`)
	e := st.add(sc, entity.StatusDraft, `{"a":1}`)
	e.Schema = sc
	svc := entity.NewService(nil, versionedStatusRepo{memRepo{st: st}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	scopes := []hmauth.APIKeyScope{hmauth.ScopeReadEntities, hmauth.ScopeWriteEntities}
	r := &rig{st: st, sess: session(t, newTestServer(t, scopes, Deps{Entities: svc, BuilderEntities: st, PageURL: func(context.Context, *entity.Entity) string { return "/p" }}))}
	return r, e
}

func TestEntitiesUpdateVersion(t *testing.T) {
	r, e := newVersionRig(t)
	id := e.ID.String()

	text, isErr := r.call(t, "entities_update", map[string]any{"id": id, "attributes": map[string]any{"a": 2}, "version": 2})
	if !isErr || !strings.HasPrefix(text, "conflict:") || !strings.Contains(text, "stale version 2; current version is 3") {
		t.Fatalf("stale: err=%v %q", isErr, text)
	}
	if got := string(r.st.ents[e.ID].Attributes); got != `{"a":1}` || r.st.ents[e.ID].Version != 3 {
		t.Errorf("stale update stored: %s v%d", got, r.st.ents[e.ID].Version)
	}

	text, isErr = r.call(t, "entities_update", map[string]any{"id": id, "attributes": map[string]any{"a": 2}, "version": 3})
	if isErr || r.st.ents[e.ID].Version != 4 {
		t.Fatalf("current: err=%v v%d %q", isErr, r.st.ents[e.ID].Version, text)
	}

	text, isErr = r.call(t, "entities_update", map[string]any{"id": id, "attributes": map[string]any{"a": 3}})
	if isErr || r.st.ents[e.ID].Version != 5 {
		t.Fatalf("omitted: err=%v v%d %q", isErr, r.st.ents[e.ID].Version, text)
	}
}

func TestEntitiesPublishVersion(t *testing.T) {
	r, e := newVersionRig(t)
	id := e.ID.String()

	text, isErr := r.call(t, "entities_publish", map[string]any{"id": id, "version": 2})
	if !isErr || !strings.HasPrefix(text, "conflict:") || !strings.Contains(text, "stale version 2; current version is 3") {
		t.Fatalf("stale: err=%v %q", isErr, text)
	}
	if st := r.st.ents[e.ID].Status; st != entity.StatusDraft {
		t.Errorf("stale publish changed the status to %s", st)
	}

	if text, isErr = r.call(t, "entities_publish", map[string]any{"id": id, "version": 3}); isErr {
		t.Fatal(text)
	}
	if st := r.st.ents[e.ID].Status; st != entity.StatusPublished {
		t.Errorf("current: status %s", st)
	}

	r.st.ents[e.ID].Status = entity.StatusDraft
	if text, isErr = r.call(t, "entities_publish", map[string]any{"id": id}); isErr {
		t.Fatal(text)
	}
	if st := r.st.ents[e.ID].Status; st != entity.StatusPublished {
		t.Errorf("omitted: status %s", st)
	}
}
