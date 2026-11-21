package mcpserver

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"encoding/json/jsontext"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hatamoto.dev/core/authz"
	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authztest"
	"hatamoto.dev/core/internal/entity"
)

// statusRepo adds the status write a publish makes to memRepo.
type statusRepo struct{ memRepo }

func (r statusRepo) UpdateStatus(ctx context.Context, id uuid.UUID, _ *uuid.UUID, st entity.EntityStatus) (*entity.Entity, error) {
	return r.st.UpdateEntityStatus(ctx, id, nil, st)
}

// contentRig runs the real entity service behind a key created by a user with role. The key's creator is
// fakeOwner, so an entity is "own" when its CreatedBy is fakeOwner.
type contentRig struct {
	st   *memStore
	sess *mcp.ClientSession
}

func newContentRig(t *testing.T, role string) *contentRig {
	t.Helper()
	st := newMemStore()
	svc := entity.NewService(nil, statusRepo{memRepo{st: st}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	scopes := []hmauth.APIKeyScope{hmauth.ScopeAll}
	ts := newTestServerRole(t, role, scopes, Deps{Entities: svc, PageURL: func(context.Context, *entity.Entity) string { return "/p" }})
	return &contentRig{st: st, sess: session(t, ts)}
}

// page adds a draft page created by creator (nil: no creator).
func (r *contentRig) page(creator *uuid.UUID) *entity.Entity {
	sc := r.st.schema("page", true, `{}`)
	e := r.st.add(sc, entity.StatusDraft, `{"page_doc":`+heroDoc+`}`)
	e.Schema = sc
	e.CreatedBy = creator
	return e
}

func (r *contentRig) update(t *testing.T, e *entity.Entity) (string, bool) {
	t.Helper()
	return call(t, r.sess, "entities_update", map[string]any{"id": e.ID.String(), "name": "New name",
		"attributes": map[string]any{"page_doc": jsontext.Value(heroDoc), "meta_title": "T"}})
}

func (r *contentRig) apply(t *testing.T, e *entity.Entity) (string, bool) {
	t.Helper()
	return call(t, r.sess, "builder_apply_ops", map[string]any{"entity_id": e.ID.String(), "version": e.Version, "ops": []map[string]any{
		{"op": "setProp", "id": "h", "key": "text", "value": "Changed"}}})
}

func TestAuthorKeyEditsOnlyOwnEntities(t *testing.T) {
	other := uuid.New()
	r := newContentRig(t, "author")
	own, theirs := r.page(&fakeOwner), r.page(&other)

	if text, isErr := r.update(t, own); isErr {
		t.Errorf("update of own entity: %s", text)
	}
	text, isErr := r.update(t, theirs)
	if !isErr || !strings.HasPrefix(text, "forbidden: you cannot change this entry") {
		t.Errorf("update of another user's entity: err=%v %q", isErr, text)
	}
	if n := len(r.st.updates); n != 1 {
		t.Errorf("store updates = %d, want 1 (the own entity only)", n)
	}

	// The builder tool passes the key gate: the entity service is what refuses.
	before := len(r.st.updates)
	text, isErr = r.apply(t, theirs)
	if !isErr || !strings.HasPrefix(text, "forbidden:") {
		t.Errorf("builder_apply_ops on another user's page: err=%v %q", isErr, text)
	}
	if len(r.st.updates) != before {
		t.Error("a refused builder save wrote")
	}
}

func TestAuthorKeyPublishesOwnEntities(t *testing.T) {
	other := uuid.New()
	r := newContentRig(t, "author")
	own, theirs := r.page(&fakeOwner), r.page(&other)

	if text, isErr := call(t, r.sess, "entities_publish", map[string]any{"id": own.ID.String()}); isErr {
		t.Errorf("publish own: %s", text)
	}
	text, isErr := call(t, r.sess, "entities_publish", map[string]any{"id": theirs.ID.String()})
	if !isErr || !strings.HasPrefix(text, "forbidden:") {
		t.Errorf("publish another's: err=%v %q", isErr, text)
	}
	if got := r.st.ents[theirs.ID].Status; got != entity.StatusDraft {
		t.Errorf("refused publish changed the status to %s", got)
	}
}

func TestContributorKeyCannotPublish(t *testing.T) {
	r := newContentRig(t, "contributor")
	own := r.page(&fakeOwner)

	text, isErr := call(t, r.sess, "entities_publish", map[string]any{"id": own.ID.String()})
	if !isErr || !strings.HasPrefix(text, "forbidden:") {
		t.Errorf("contributor publish: err=%v %q", isErr, text)
	}
	if text, isErr := r.update(t, own); isErr {
		t.Errorf("contributor edit of own draft: %s", text)
	}
	if got := r.st.ents[own.ID].Status; got != entity.StatusDraft {
		t.Errorf("status = %s, want draft", got)
	}
}

func TestReviewerKeyPublishesAndEditsAnyEntity(t *testing.T) {
	other := uuid.New()
	r := newContentRig(t, "reviewer")
	theirs, orphan := r.page(&other), r.page(nil)

	for _, e := range []*entity.Entity{theirs, orphan} {
		if text, isErr := r.update(t, e); isErr {
			t.Errorf("reviewer update: %s", text)
		}
		if text, isErr := call(t, r.sess, "entities_publish", map[string]any{"id": e.ID.String()}); isErr {
			t.Errorf("reviewer publish: %s", text)
		}
		if text, isErr := call(t, r.sess, "entities_unpublish", map[string]any{"id": e.ID.String()}); isErr {
			t.Errorf("reviewer unpublish: %s", text)
		}
	}
}

// The subject reaches the entity service: the Recorder sees an entity Object with a kind and an ID, asked
// for the key's creator.
func TestMCPPutsTheKeyOnTheContext(t *testing.T) {
	rec := &authztest.Recorder{}
	authztest.Use(t, rec)
	r := newContentRig(t, "author")
	e := r.page(&fakeOwner)
	if text, isErr := r.update(t, e); isErr {
		t.Fatal(text)
	}
	for _, c := range rec.Calls() {
		if c.Action == authz.ActionEntityUpdate && c.Object.Kind == "entity" && c.Object.ID == e.ID.String() {
			if c.Subject.UserID != fakeOwner.String() || c.Subject.KeyID == "" {
				t.Errorf("subject = %+v, want the key and its creator", c.Subject)
			}
			return
		}
	}
	t.Errorf("no entity update question for %s in %+v", e.ID, rec.Calls())
}
