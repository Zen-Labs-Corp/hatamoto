package entity

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"hatamoto.dev/core/internal/testutil"
)

type recordingSink struct {
	mu     sync.Mutex
	events []Event
}

func (r *recordingSink) Emit(_ context.Context, ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingSink) kinds() []EventKind {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]EventKind, 0, len(r.events))
	for _, e := range r.events {
		out = append(out, e.Kind)
	}
	return out
}

func TestService_Events(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("create emits created with schema slug", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		schema := seedSchema(t, sr)
		sink := &recordingSink{}
		svc.SetEvents(sink)

		ent, err := svc.CreateEntity(ctx, &testUserID, &CreateEntityInput{
			SchemaID: schema.ID, Name: "Hi", Attributes: json.RawMessage(`{"title":"Hi"}`),
		})
		testutil.RequireNoError(t, err)
		testutil.RequireEqual(t, []EventKind{EventCreated}, sink.kinds())
		testutil.Equal(t, ent.ID, sink.events[0].ID)
		testutil.Equal(t, schema.Slug, sink.events[0].SchemaSlug)
	})

	t.Run("update emits updated", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		existing := seedEntity(t, er, seedSchema(t, sr))
		sink := &recordingSink{}
		svc.SetEvents(sink)

		name := "Renamed"
		_, err := svc.UpdateEntity(ctx, existing.ID, &testUserID, &UpdateEntityInput{Name: &name})
		testutil.RequireNoError(t, err)
		testutil.Equal(t, []EventKind{EventUpdated}, sink.kinds())
	})

	t.Run("status: published only on transition", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		existing := seedEntity(t, er, seedSchema(t, sr))
		sink := &recordingSink{}
		svc.SetEvents(sink)

		_, err := svc.UpdateEntityStatus(ctx, existing.ID, &testUserID, StatusPublished)
		testutil.RequireNoError(t, err)
		_, err = svc.UpdateEntityStatus(ctx, existing.ID, &testUserID, StatusPublished)
		testutil.RequireNoError(t, err)
		_, err = svc.UpdateEntityStatus(ctx, existing.ID, &testUserID, StatusArchived)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, []EventKind{EventPublished, EventUpdated, EventUpdated}, sink.kinds())
		for _, ev := range sink.events {
			testutil.Equal(t, existing.Schema.Slug, ev.SchemaSlug, "status events keep the schema slug")
		}
	})

	t.Run("delete emits deleted with pre-read name", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		existing := seedEntity(t, er, seedSchema(t, sr))
		sink := &recordingSink{}
		svc.SetEvents(sink)

		testutil.RequireNoError(t, svc.DeleteEntity(ctx, existing.ID))
		testutil.RequireEqual(t, []EventKind{EventDeleted}, sink.kinds())
		testutil.Equal(t, existing.Name, sink.events[0].Name)
		testutil.Equal(t, existing.Schema.Slug, sink.events[0].SchemaSlug)
	})

	t.Run("failed mutation emits nothing", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		existing := seedEntity(t, er, seedSchema(t, sr))
		sink := &recordingSink{}
		svc.SetEvents(sink)
		er.statusErr = errors.New("boom")
		er.deleteErr = errors.New("boom")

		_, err := svc.UpdateEntityStatus(ctx, existing.ID, &testUserID, StatusPublished)
		testutil.RequireError(t, err)
		testutil.RequireError(t, svc.DeleteEntity(ctx, existing.ID))
		testutil.Empty(t, sink.kinds())
	})
}

func TestService_BeforeSave(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("hook can rewrite name and attributes on create", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		schema := seedSchema(t, sr)
		svc.SetBeforeSave(func(_ context.Context, in *BeforeSaveInput) error {
			testutil.Equal(t, "create", in.Operation)
			testutil.Nil(t, in.ID)
			in.Name = "Rewritten"
			in.Attributes = json.RawMessage(`{"title":"Plugin"}`)
			return nil
		})

		ent, err := svc.CreateEntity(ctx, &testUserID, &CreateEntityInput{
			SchemaID: schema.ID, Name: "Orig", Attributes: json.RawMessage(`{"title":"Orig"}`),
		})
		testutil.RequireNoError(t, err)
		testutil.Equal(t, "Rewritten", ent.Name)
		testutil.JSONEqual(t, `{"title":"Plugin"}`, string(ent.Attributes))
	})

	t.Run("hook error rejects the save", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		sink := &recordingSink{}
		svc.SetEvents(sink)
		svc.SetBeforeSave(func(context.Context, *BeforeSaveInput) error { return errors.New("nope") })

		_, err := svc.CreateEntity(ctx, &testUserID, &CreateEntityInput{
			SchemaID: schema.ID, Name: "X", Attributes: json.RawMessage(`{"title":"X"}`),
		})
		testutil.RequireErrorIs(t, err, ErrRejectedByPlugin)
		testutil.Contains(t, err.Error(), "nope")
		testutil.Empty(t, er.entities)
		testutil.Empty(t, sink.kinds())
	})

	t.Run("plugin-changed attributes are re-validated", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		existing := seedEntity(t, er, seedSchema(t, sr))
		svc.SetBeforeSave(func(_ context.Context, in *BeforeSaveInput) error {
			in.Attributes = json.RawMessage(`{"title": 123}`) // schema wants a string
			return nil
		})

		_, err := svc.UpdateEntity(ctx, existing.ID, &testUserID, &UpdateEntityInput{})
		testutil.RequireError(t, err)
		testutil.NotErrorIs(t, err, ErrRejectedByPlugin)
	})

	t.Run("plugin-changed attributes are re-sanitized", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		existing := seedEntity(t, er, seedSchema(t, sr))
		svc.SetBeforeSave(func(_ context.Context, in *BeforeSaveInput) error {
			in.Attributes = json.RawMessage(`{"title":"t","content":"<p>ok</p><script>alert(1)</script>"}`)
			return nil
		})

		ent, err := svc.UpdateEntity(ctx, existing.ID, &testUserID, &UpdateEntityInput{})
		testutil.RequireNoError(t, err)
		testutil.NotContains(t, string(ent.Attributes), "<script")
	})

	t.Run("restore runs before_save and emits updated", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		existing := seedEntity(t, er, seedSchema(t, sr))
		_, err := er.CreateVersion(ctx, existing.ID, 1, existing.Attributes, &testUserID, nil)
		testutil.RequireNoError(t, err)
		sink := &recordingSink{}
		svc.SetEvents(sink)
		var op string
		svc.SetBeforeSave(func(_ context.Context, in *BeforeSaveInput) error { op = in.Operation; return nil })

		_, err = svc.RestoreVersion(ctx, existing.ID, &testUserID, 1)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, "restore", op)
		testutil.Equal(t, []EventKind{EventUpdated}, sink.kinds())
	})
}
