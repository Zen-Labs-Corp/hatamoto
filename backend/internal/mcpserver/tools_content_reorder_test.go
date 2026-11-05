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
	"hatamoto.dev/core/internal/taxonomy"
)

func TestReorderToolsNeedTheirWriteScope(t *testing.T) {
	for tool, scope := range map[string]hmauth.APIKeyScope{
		"entities_reorder": hmauth.ScopeReadEntities,
		"terms_reorder":    hmauth.ScopeReadEntities,
	} {
		text, isErr := callSEO(t, []hmauth.APIKeyScope{scope}, Deps{}, tool, map[string]any{"id": uuid.NewString(), "position": 1})
		if !isErr || strings.TrimSpace(text) == "" {
			t.Errorf("%s with a read-only key = %v %q, want a refusal", tool, isErr, text)
		}
	}
	// A terms write key may not reorder entities, and the reverse.
	if _, isErr := callSEO(t, []hmauth.APIKeyScope{hmauth.ScopeWriteTerms}, Deps{}, "entities_reorder", map[string]any{"id": uuid.NewString(), "position": 1}); !isErr {
		t.Error("entities_reorder accepted a terms-only key")
	}
	if _, isErr := callSEO(t, []hmauth.APIKeyScope{hmauth.ScopeWriteEntities}, Deps{}, "terms_reorder", map[string]any{"id": uuid.NewString(), "position": 1}); !isErr {
		t.Error("terms_reorder accepted an entities-only key")
	}
}

func TestReorderToolsRejectBadIDs(t *testing.T) {
	text, isErr := callSEO(t, []hmauth.APIKeyScope{hmauth.ScopeWriteEntities}, Deps{}, "entities_reorder", map[string]any{"id": "nope", "position": 1})
	if !isErr || !strings.Contains(text, "id") {
		t.Errorf("bad id = %v %q", isErr, text)
	}
}

// reorderRepo is the two calls a pure reorder makes besides the embedded memRepo's.
// Any other repository call panics on the nil interface: a pure reorder must
// never edit the entity (an edit is what hands a plugin's page to its owner),
// and a take-over would show as a second audit row.
type reorderRepo struct {
	memRepo
	placed []int
}

func (r *reorderRepo) SetPosition(_ context.Context, _ uuid.UUID, pos int) (int, int, error) {
	r.placed = append(r.placed, pos)
	return pos, 5, nil
}

func (r *reorderRepo) OrderRows(context.Context, uuid.UUID, string) ([]*entity.Entity, error) {
	return nil, nil
}

func TestEntitiesReorderSucceedsAuditsOnceAndNeverTakesAPageOver(t *testing.T) {
	st := newMemStore()
	page := st.add(st.schema("page", true, `{}`), entity.StatusPublished, `{}`)
	page.Schema = st.schemas[page.SchemaID]
	repo := &reorderRepo{memRepo: memRepo{st: st}}
	svc := entity.NewService(nil, repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	audit := &recAudit{}
	sess := session(t, newTestServer(t, []hmauth.APIKeyScope{hmauth.ScopeWriteEntities}, Deps{Entities: svc, BuilderEntities: st, Audit: audit}))
	text, isErr := call(t, sess, "entities_reorder", map[string]any{"id": page.ID.String(), "position": 3})
	if isErr {
		t.Fatalf("entities_reorder: %s", text)
	}
	if len(repo.placed) != 1 || repo.placed[0] != 3 {
		t.Errorf("positions set = %v, want [3]", repo.placed)
	}
	if len(st.updates) != 0 {
		t.Errorf("a pure reorder edited the entity: %v", st.updates)
	}
	if len(audit.entries) != 1 || audit.entries[0].Tool != "entities_reorder" || audit.entries[0].Err != nil {
		t.Errorf("audit = %+v, want one entities_reorder row", audit.entries)
	}
}

type reorderTerms struct {
	taxonomy.Repository
	term   *taxonomy.Term
	placed []int
}

func (r *reorderTerms) GetTerm(context.Context, uuid.UUID) (*taxonomy.Term, error) {
	c := *r.term
	return &c, nil
}

func (r *reorderTerms) SetPosition(_ context.Context, _ uuid.UUID, pos int) (int, int, error) {
	r.placed = append(r.placed, pos)
	return pos, 4, nil
}

func TestTermsReorderSucceedsAuditsOnceAndPurges(t *testing.T) {
	repo := &reorderTerms{term: &taxonomy.Term{ID: uuid.New(), Kind: "tag", Name: "Go", Slug: "go"}}
	svc := taxonomy.NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	audit := &recAudit{}
	changed := 0
	d := Deps{Taxonomy: svc, Audit: audit, Changed: func(context.Context) { changed++ }}
	sess := session(t, newTestServer(t, []hmauth.APIKeyScope{hmauth.ScopeWriteTerms}, d))
	text, isErr := call(t, sess, "terms_reorder", map[string]any{"id": repo.term.ID.String(), "position": 2})
	if isErr {
		t.Fatalf("terms_reorder: %s", text)
	}
	if len(repo.placed) != 1 || repo.placed[0] != 2 || changed != 1 {
		t.Errorf("positions = %v, changed = %d, want [2] and 1", repo.placed, changed)
	}
	if len(audit.entries) != 1 || audit.entries[0].Tool != "terms_reorder" {
		t.Errorf("audit = %+v, want one terms_reorder row", audit.entries)
	}
}
