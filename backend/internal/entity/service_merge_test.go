package entity

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/testutil"
)

func mergeInput(patch string) *UpdateEntityInput {
	v := jsontext.Value(patch)
	return &UpdateEntityInput{Attributes: &v, MergeAttributes: true}
}

func TestService_UpdateEntity_MergeAttributes(t *testing.T) {
	t.Parallel()

	t.Run("keeps untouched attributes and stores one version", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		existing := seedEntity(t, er, seedSchema(t, sr))
		updated, err := svc.UpdateEntity(context.Background(), existing.ID, &testUserID, mergeInput(`{"body":"New"}`))
		testutil.RequireNoError(t, err)
		testutil.JSONEqual(t, `{"title":"Hello","body":"New"}`, string(updated.Attributes))
		testutil.Equal(t, 2, updated.Version)
	})

	t.Run("null removes a key", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		existing := seedEntity(t, er, seedSchema(t, sr))
		updated, err := svc.UpdateEntity(context.Background(), existing.ID, &testUserID, mergeInput(`{"body":null}`))
		testutil.RequireNoError(t, err)
		testutil.JSONEqual(t, `{"title":"Hello"}`, string(updated.Attributes))
	})

	t.Run("removing a required attribute is refused and names it", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr, func(s *EntitySchema) { s.Schema = strictSchemaJSON })
		existing := seedEntity(t, er, schema)
		_, err := svc.UpdateEntity(context.Background(), existing.ID, &testUserID, mergeInput(`{"title":null}`))
		if !errors.Is(err, ErrValidationFailed) || !strings.Contains(err.Error(), "title") {
			t.Fatalf("err = %v, want ErrValidationFailed naming title", err)
		}
		got, _ := er.GetByID(context.Background(), existing.ID)
		testutil.JSONEqual(t, `{"title":"Hello","body":"World"}`, string(got.Attributes))
		testutil.Equal(t, 1, got.Version)
	})

	t.Run("null patch is refused", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		existing := seedEntity(t, er, seedSchema(t, sr))
		_, err := svc.UpdateEntity(context.Background(), existing.ID, &testUserID, mergeInput(`null`))
		if !errors.Is(err, ErrValidationFailed) {
			t.Fatalf("err = %v, want ErrValidationFailed", err)
		}
		got, _ := er.GetByID(context.Background(), existing.ID)
		testutil.JSONEqual(t, `{"title":"Hello","body":"World"}`, string(got.Attributes))
		testutil.Equal(t, 1, got.Version)
	})

	t.Run("without the flag attributes are replaced", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		existing := seedEntity(t, er, seedSchema(t, sr))
		in := mergeInput(`{"body":"New"}`)
		in.MergeAttributes = false
		updated, err := svc.UpdateEntity(context.Background(), existing.ID, &testUserID, in)
		testutil.RequireNoError(t, err)
		testutil.JSONEqual(t, `{"body":"New"}`, string(updated.Attributes))
	})

	t.Run("flag is cleared after the call", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		existing := seedEntity(t, er, seedSchema(t, sr))
		in := mergeInput(`{"body":"New"}`)
		_, err := svc.UpdateEntity(context.Background(), existing.ID, &testUserID, in)
		testutil.RequireNoError(t, err)
		if in.MergeAttributes {
			t.Error("MergeAttributes still set after UpdateEntity")
		}
	})

	t.Run("a no-op merge does not take ownership", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		existing := seedEntity(t, er, seedSchema(t, sr))
		in := mergeInput(`{"title":"Hello"}`)
		_, err := svc.UpdateEntity(context.Background(), existing.ID, &testUserID, in)
		testutil.RequireNoError(t, err)
		if in.TakeOwnership {
			t.Error("TakeOwnership set by a merge that changed nothing")
		}
	})
}
