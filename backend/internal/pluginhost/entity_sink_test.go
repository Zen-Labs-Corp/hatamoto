package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/sdk"
)

// Embedding the interfaces lets the fakes implement only what the service
// calls; anything else panics, which is the right failure for a test.
type sinkSchemaRepo struct {
	entity.SchemaRepository
	schema *entity.EntitySchema
}

func (f *sinkSchemaRepo) GetByID(context.Context, uuid.UUID) (*entity.EntitySchema, error) {
	return f.schema, nil
}

type sinkEntityRepo struct {
	entity.EntityRepository
	ent *entity.Entity
}

func (f *sinkEntityRepo) Create(_ context.Context, _ *uuid.UUID, in *entity.CreateEntityInput) (*entity.Entity, error) {
	f.ent = &entity.Entity{ID: uuid.New(), SchemaID: in.SchemaID, Name: in.Name, Slug: in.Slug,
		Status: entity.StatusDraft, Attributes: in.Attributes, Version: 1}
	return f.ent, nil
}

func (f *sinkEntityRepo) GetByID(context.Context, uuid.UUID) (*entity.Entity, error) {
	cp := *f.ent
	return &cp, nil
}

func (f *sinkEntityRepo) UpdateStatus(_ context.Context, _ uuid.UUID, _ *uuid.UUID, st entity.EntityStatus) (*entity.Entity, error) {
	f.ent.Status = st
	cp := *f.ent
	return &cp, nil
}

func (f *sinkEntityRepo) Delete(context.Context, uuid.UUID) error { return nil }

func newSinkRig(t *testing.T) (*entity.Service, *hooks.Engine, *sinkSchemaRepo) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := hooks.New(log)
	schema := &entity.EntitySchema{ID: uuid.New(), Slug: "post", Name: "Post", Schema: json.RawMessage(`{"type":"object"}`)}
	sr := &sinkSchemaRepo{schema: schema}
	svc := entity.NewService(sr, &sinkEntityRepo{}, log)
	svc.SetEvents(NewEntitySink(engine, log))
	svc.SetBeforeSave(BeforeSave(engine))
	return svc, engine, sr
}

// A publish through the real service fires the plugin action once.
func TestPublishFiresActionOnce(t *testing.T) {
	svc, engine, sr := newSinkRig(t)
	ctx := context.Background()

	var mu sync.Mutex
	var got []sdk.EntityEvent
	hooks.OnAction(engine.For("test"), sdk.EntityPublished, 10, func(_ context.Context, ev sdk.EntityEvent) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, ev)
	})

	ent, err := svc.CreateEntity(ctx, nil, &entity.CreateEntityInput{SchemaID: sr.schema.ID, Name: "Hello", Attributes: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateEntityStatus(ctx, ent.ID, nil, entity.StatusPublished); err != nil {
		t.Fatal(err)
	}
	engine.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].SchemaSlug != "post" || got[0].Name != "Hello" || got[0].ID != ent.ID.String() {
		t.Fatalf("action payloads = %+v", got)
	}
}

func TestCreateAndDeleteFireActions(t *testing.T) {
	svc, engine, sr := newSinkRig(t)
	ctx := context.Background()
	var mu sync.Mutex
	var calls []string
	rec := func(name string) func(context.Context, sdk.EntityEvent) {
		return func(context.Context, sdk.EntityEvent) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, name)
		}
	}
	hooks.OnAction(engine.For("test"), sdk.EntityCreated, 10, rec("created"))
	hooks.OnAction(engine.For("test"), sdk.EntityDeleted, 10, rec("deleted"))
	ent, err := svc.CreateEntity(ctx, nil, &entity.CreateEntityInput{SchemaID: sr.schema.ID, Name: "A", Attributes: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteEntity(ctx, ent.ID); err != nil {
		t.Fatal(err)
	}
	engine.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("calls = %v", calls)
	}
}

func TestBeforeSaveFilterRewritesAndRejects(t *testing.T) {
	svc, engine, sr := newSinkRig(t)
	ctx := context.Background()
	in := func() *entity.CreateEntityInput {
		return &entity.CreateEntityInput{SchemaID: sr.schema.ID, Name: "Orig", Attributes: json.RawMessage(`{}`)}
	}

	hooks.OnFilter(engine.For("rewrite"), sdk.EntityBeforeSave, 10, func(_ context.Context, d sdk.EntityDraft) (sdk.EntityDraft, error) {
		if d.Operation != "create" || d.SchemaSlug != "post" {
			t.Errorf("draft = %+v", d)
		}
		d.Name = "Rewritten"
		return d, nil
	})
	ent, err := svc.CreateEntity(ctx, nil, in())
	if err != nil || ent.Name != "Rewritten" {
		t.Fatalf("ent=%+v err=%v", ent, err)
	}

	hooks.OnFilter(engine.For("reject"), sdk.EntityBeforeSave, 20, func(_ context.Context, d sdk.EntityDraft) (sdk.EntityDraft, error) {
		return d, &sdk.Reject{Reason: "no"}
	})
	if _, err := svc.CreateEntity(ctx, nil, in()); !errors.Is(err, entity.ErrRejectedByPlugin) {
		t.Fatalf("err = %v", err)
	}
}

func TestBeforeSaveNonRejectErrorFailsOpen(t *testing.T) {
	svc, engine, sr := newSinkRig(t)
	hooks.OnFilter(engine.For("flaky"), sdk.EntityBeforeSave, 10, func(_ context.Context, d sdk.EntityDraft) (sdk.EntityDraft, error) {
		d.Name = "Dropped"
		return d, errors.New("backend down")
	})
	ent, err := svc.CreateEntity(context.Background(), nil,
		&entity.CreateEntityInput{SchemaID: sr.schema.ID, Name: "Orig", Attributes: json.RawMessage(`{}`)})
	if err != nil || ent.Name != "Orig" {
		t.Fatalf("want save to proceed unchanged, ent=%+v err=%v", ent, err)
	}
}

const (
	sinkDoc    = `{"v":2,"nodes":[{"id":"s","t":"container","p":{"tag":"section","v1":true},"c":[{"id":"k","t":"component","p":{"type":"content","content":{"body":"fresh"}}}]}]}`
	sinkStale  = `[{"id":"k","type":"content","sort_order":0,"content":{"body":"stale"}}]`
	sinkStored = `{"page_doc":` + sinkDoc + `,"page_sections":` + sinkStale + `}`
)

// The before_save hook is shown page_sections derived from page_doc, not the
// frozen mirror; a hook that leaves it alone does not disturb the stored one.
func TestBeforeSaveSeesDerivedSections(t *testing.T) {
	_, engine, _ := newSinkRig(t)
	var seen sdk.EntityDraft
	hooks.OnFilter(engine.For("peek"), sdk.EntityBeforeSave, 10, func(_ context.Context, d sdk.EntityDraft) (sdk.EntityDraft, error) {
		seen = d
		d.Name = "Renamed" // an edit elsewhere must not drag the derived sections in
		return d, nil
	})
	in := &entity.BeforeSaveInput{Operation: "update", SchemaSlug: "page", Name: "N", Attributes: json.RawMessage(sinkStored)}
	if err := BeforeSave(engine)(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if !containsAll(string(seen.Attributes), `"fresh"`) {
		t.Fatalf("hook saw %s, want derived sections", seen.Attributes)
	}
	if containsAll(string(seen.Attributes), `"stale"`) {
		t.Fatalf("hook saw the frozen mirror: %s", seen.Attributes)
	}
	if !jsonEqual(in.Attributes, json.RawMessage(sinkStored)) || in.Name != "Renamed" {
		t.Fatalf("untouched sections must come back as stored: %s", in.Attributes)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, x := range subs {
		if !strings.Contains(s, x) {
			return false
		}
	}
	return true
}

// A hook that edits page_sections keeps its edit in the output (the service
// seam then discards it); the helper only undoes the unedited derived view.
func TestKeepStoredSectionsKeepsRealEdit(t *testing.T) {
	stored := json.RawMessage(sinkStored)
	shown := entity.DerivedAttributes(stored)
	edited := json.RawMessage(`{"page_doc":` + sinkDoc + `,"page_sections":[]}`)
	if out := keepStoredSections(stored, shown, edited); !jsonEqual(out, edited) {
		t.Fatalf("a real edit was undone: %s", out)
	}
}

// entities.get hands plugins page_sections derived from page_doc, and none for
// a builder-only page.
func TestEntityDTODerivesSections(t *testing.T) {
	a := &API{}
	sc := &entity.EntitySchema{Slug: "page"}
	dto := a.entityDTO(context.Background(), &entity.Entity{ID: uuid.New(), Schema: sc, Attributes: json.RawMessage(sinkStored)})
	if !containsAll(string(dto.Attributes), `"fresh"`) || containsAll(string(dto.Attributes), `"stale"`) {
		t.Errorf("v1 doc: attributes = %s", dto.Attributes)
	}
	builderOnly := `{"page_doc":{"v":2,"nodes":[{"id":"r","t":"container","p":{"tag":"div"}}]},"page_sections":` + sinkStale + `}`
	dto = a.entityDTO(context.Background(), &entity.Entity{ID: uuid.New(), Schema: sc, Attributes: json.RawMessage(builderOnly)})
	if containsAll(string(dto.Attributes), "page_sections") || !containsAll(string(dto.Attributes), "page_doc") {
		t.Errorf("builder-only: attributes = %s", dto.Attributes)
	}
}

// A scheduled publish reaches the sink without a locale; the lookup fills it.
func TestEntityPublishedLocaleLookup(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	id := uuid.New()
	for _, tc := range []struct {
		name   string
		lookup func(context.Context, uuid.UUID) string
		want   string
	}{
		{"set", func(_ context.Context, got uuid.UUID) string {
			if got != id {
				t.Errorf("lookup id = %s, want %s", got, id)
			}
			return "fr"
		}, "fr"},
		{"unset", nil, ""},
		{"error reads as empty", func(context.Context, uuid.UUID) string { return "" }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := hooks.New(log)
			sink := NewEntitySink(engine, log)
			if tc.lookup != nil {
				sink.SetLocaleLookup(tc.lookup)
			}
			var mu sync.Mutex
			var got []sdk.EntityEvent
			hooks.OnAction(engine.For("test"), sdk.EntityPublished, 10, func(_ context.Context, ev sdk.EntityEvent) {
				mu.Lock()
				defer mu.Unlock()
				got = append(got, ev)
			})
			sink.EntityPublished(context.Background(), id, "post", "hello", "Hello", 2)
			engine.Wait()
			mu.Lock()
			defer mu.Unlock()
			if len(got) != 1 || got[0].Locale != tc.want {
				t.Fatalf("events = %+v, want locale %q", got, tc.want)
			}
		})
	}
}

// The before_save draft carries the entity's locale for plugins to read; the
// host never reads it back.
func TestBeforeSaveDraftCarriesLocale(t *testing.T) {
	svc, engine, sr := newSinkRig(t)
	var seen []string
	hooks.OnFilter(engine.For("spy"), sdk.EntityBeforeSave, 10, func(_ context.Context, d sdk.EntityDraft) (sdk.EntityDraft, error) {
		seen = append(seen, d.Locale)
		d.Locale = "zz" // ignored by the host
		return d, nil
	})
	if _, err := svc.CreateEntity(context.Background(), nil,
		&entity.CreateEntityInput{SchemaID: sr.schema.ID, Name: "Orig", Locale: "FR", Attributes: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != "fr" {
		t.Fatalf("draft locales = %v, want [fr]", seen)
	}
}
