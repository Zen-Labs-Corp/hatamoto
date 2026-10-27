package entity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/testutil"
)

func rawSecs(body string) string {
	return `[{"id":"a","type":"content","content":{"body":"<p>` + body + `</p>"},"sort_order":0}]`
}

func attrsWith(parts ...string) json.RawMessage {
	return json.RawMessage(`{"title":"T"` + strings.Join(append([]string{""}, parts...), ",") + `}`)
}

// v1Doc is the doc a v1 page with one section "body" converts to.
func v1Doc(t *testing.T, body string) string {
	t.Helper()
	return fromSecs(t, rawSecs(body))
}

func hasKey(t *testing.T, raw json.RawMessage, key string) bool {
	t.Helper()
	var m map[string]json.RawMessage
	testutil.RequireNoError(t, json.Unmarshal(raw, &m))
	_, ok := m[key]
	return ok
}

func TestService_PageDoc_Create(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("doc write stores no page_sections", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		schema := seedSchema(t, sr)
		for i, doc := range []string{twoCol, v1Doc(t, "hello")} {
			e, err := svc.CreateEntity(ctx, &testUserID, &CreateEntityInput{SchemaID: schema.ID, Name: fmt.Sprintf("P%d", i),
				Attributes: attrsWith(`"page_doc":`+doc, `"page_sections":`+rawSecs("x"))})
			testutil.RequireNoError(t, err)
			testutil.True(t, hasKey(t, e.Attributes, "page_doc"), "doc kept")
			testutil.True(t, !hasKey(t, e.Attributes, "page_sections"), "sections stored: %s", e.Attributes)
		}
	})

	t.Run("invalid doc is ErrInvalidDoc", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		schema := seedSchema(t, sr)
		_, err := svc.CreateEntity(ctx, &testUserID, &CreateEntityInput{SchemaID: schema.ID, Name: "P",
			Attributes: attrsWith(`"page_doc":{"v":3}`)})
		testutil.True(t, errors.Is(err, ErrInvalidDoc) && !errors.Is(err, ErrValidationFailed), "err=%v", err)
	})

	t.Run("sections only: converted once, no page_sections stored", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		schema := seedSchema(t, sr)
		e, err := svc.CreateEntity(ctx, &testUserID, &CreateEntityInput{SchemaID: schema.ID, Name: "P", Attributes: attrsWith(`"page_sections":` + rawSecs("x"))})
		testutil.RequireNoError(t, err)
		testutil.True(t, hasKey(t, e.Attributes, "page_doc") && !hasKey(t, e.Attributes, "page_sections"), "attrs=%s", e.Attributes)
	})

	t.Run("hook-added doc is validated", func(t *testing.T) {
		t.Parallel()
		svc, sr, _ := newTestService()
		schema := seedSchema(t, sr)
		svc.SetBeforeSave(func(_ context.Context, in *BeforeSaveInput) error {
			in.Attributes = attrsWith(`"page_doc":` + twoCol)
			return nil
		})
		e, err := svc.CreateEntity(ctx, &testUserID, &CreateEntityInput{SchemaID: schema.ID, Name: "P", Attributes: attrsWith()})
		testutil.RequireNoError(t, err)
		testutil.True(t, hasKey(t, e.Attributes, "page_doc"), "attrs=%s", e.Attributes)

		svc.SetBeforeSave(func(_ context.Context, in *BeforeSaveInput) error {
			in.Attributes = attrsWith(`"page_doc":{"v":9}`)
			return nil
		})
		_, err = svc.CreateEntity(ctx, &testUserID, &CreateEntityInput{SchemaID: schema.ID, Name: "Q", Attributes: attrsWith()})
		testutil.True(t, errors.Is(err, ErrInvalidDoc), "err=%v", err)
	})
}

func TestService_PageDoc_Update(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	seedConverted := func(t *testing.T) (*Service, *Entity) {
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		e := seedEntity(t, er, schema, func(e *Entity) {
			e.Attributes = attrsWith(`"page_sections":`+rawSecs("one"), `"page_doc":`+v1Doc(t, "one"))
		})
		return svc, e
	}

	t.Run("doc write leaves the stored mirror untouched", func(t *testing.T) {
		t.Parallel()
		svc, e := seedConverted(t)
		in := attrsWith(`"page_doc":` + v1Doc(t, "two"))
		got, err := svc.UpdateEntity(ctx, e.ID, &testUserID, &UpdateEntityInput{Attributes: &in})
		testutil.RequireNoError(t, err)
		s := string(got.Attributes)
		testutil.True(t, strings.Contains(s, "two") && strings.Contains(s, "one"), "attrs=%s", s) // new doc + frozen mirror
		var m map[string]json.RawMessage
		testutil.RequireNoError(t, json.Unmarshal(got.Attributes, &m))
		testutil.True(t, strings.Contains(string(m["page_sections"]), "one") && !strings.Contains(string(m["page_sections"]), "two"), "mirror=%s", m["page_sections"])
	})

	t.Run("changed page_sections is refused", func(t *testing.T) {
		t.Parallel()
		svc, e := seedConverted(t)
		in := attrsWith(`"page_sections":`+rawSecs("two"), `"page_doc":`+v1Doc(t, "one"))
		_, err := svc.UpdateEntity(ctx, e.ID, &testUserID, &UpdateEntityInput{Attributes: &in})
		testutil.True(t, errors.Is(err, ErrPageSectionsRemoved) && !errors.Is(err, ErrValidationFailed), "err=%v", err)
	})

	t.Run("no-op save is jsonb-equal to stored (no version)", func(t *testing.T) {
		t.Parallel()
		svc, e := seedConverted(t)
		stored := append(json.RawMessage(nil), e.Attributes...)
		in := append(json.RawMessage(nil), e.Attributes...)
		got, err := svc.UpdateEntity(ctx, e.ID, &testUserID, &UpdateEntityInput{Attributes: &in})
		testutil.RequireNoError(t, err)
		testutil.True(t, jsonRawEqual(got.Attributes, stored), "attrs changed: %s", got.Attributes)
	})

	t.Run("builder-only page refuses a page_sections write", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		e := seedEntity(t, er, schema, func(e *Entity) { e.Attributes = attrsWith(`"page_doc":` + twoCol) })
		in := attrsWith(`"page_sections":`+rawSecs("x"), `"page_doc":`+twoCol)
		_, err := svc.UpdateEntity(ctx, e.ID, &testUserID, &UpdateEntityInput{Attributes: &in})
		testutil.True(t, errors.Is(err, ErrPageSectionsRemoved), "err=%v", err)
	})

	t.Run("write omitting page_doc carries it forward", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		e := seedEntity(t, er, schema, func(e *Entity) { e.Attributes = attrsWith(`"page_doc":` + twoCol) })
		in := attrsWith(`"extra":1`)
		got, err := svc.UpdateEntity(ctx, e.ID, &testUserID, &UpdateEntityInput{Attributes: &in})
		testutil.RequireNoError(t, err)
		testutil.True(t, hasKey(t, got.Attributes, "page_doc"), "doc dropped: %s", got.Attributes)
	})

	t.Run("unconverted page with new sections converts them, mirror frozen", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		e := seedEntity(t, er, schema, func(e *Entity) { e.Attributes = attrsWith(`"page_sections":` + rawSecs("one")) })
		in := attrsWith(`"page_sections":` + rawSecs("two"))
		got, err := svc.UpdateEntity(ctx, e.ID, &testUserID, &UpdateEntityInput{Attributes: &in})
		testutil.RequireNoError(t, err)
		var m map[string]json.RawMessage
		testutil.RequireNoError(t, json.Unmarshal(got.Attributes, &m))
		testutil.True(t, strings.Contains(string(m["page_doc"]), "two") && strings.Contains(string(m["page_sections"]), "one"), "attrs=%s", got.Attributes)
	})
}

func TestService_PageDoc_Restore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, sr, er := newTestService()
	schema := seedSchema(t, sr)
	e := seedEntity(t, er, schema, func(e *Entity) {
		e.Attributes = attrsWith(`"page_doc":` + twoCol)
		e.Version = 2
	})
	er.versions[e.ID] = []*EntityVersion{{ID: uuid.New(), EntityID: e.ID, Version: 1,
		Attributes: attrsWith(`"page_sections":` + rawSecs("old"))}}

	got, err := svc.RestoreVersion(ctx, e.ID, &testUserID, 1)
	testutil.RequireNoError(t, err) // never an error for sections on restore
	testutil.True(t, hasKey(t, got.Attributes, "page_doc") && strings.Contains(string(got.Attributes), "old"), "attrs=%s", got.Attributes)

	// A blob with an invalid doc is rejected, not stored.
	er.versions[e.ID] = append(er.versions[e.ID], &EntityVersion{ID: uuid.New(), EntityID: e.ID, Version: 2,
		Attributes: attrsWith(`"page_doc":{"v":9}`)})
	_, err = svc.RestoreVersion(ctx, e.ID, &testUserID, 2)
	testutil.True(t, errors.Is(err, ErrInvalidDoc), "err=%v", err)
}

func TestImport_PageDoc(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, repo, _, _ := importFixture(t)
	batch := func(key, attrs string) ImportBatch {
		return ImportBatch{Source: "t", Items: []ImportItem{{Key: key, SchemaSlug: "page", Name: "N", Attributes: json.RawMessage(attrs)}}}
	}

	// a new export carries both: the doc wins, no mirror is stored
	res, err := svc.Import(ctx, &testUserID, batch("k", `{"title":"T","page_doc":`+twoCol+`,"page_sections":`+rawSecs("x")+`}`))
	testutil.RequireNoError(t, err)
	testutil.Equal(t, 1, res.Created)
	e := repo.entities[res.Items[0].ID]
	testutil.True(t, hasKey(t, e.Attributes, "page_doc") && !hasKey(t, e.Attributes, "page_sections"), "attrs=%s", e.Attributes)

	// an old export carries sections only: becomes a doc
	res, err = svc.Import(ctx, &testUserID, batch("old", `{"title":"T","page_sections":`+rawSecs("y")+`}`))
	testutil.RequireNoError(t, err)
	e = repo.entities[res.Items[0].ID]
	testutil.True(t, hasKey(t, e.Attributes, "page_doc") && !hasKey(t, e.Attributes, "page_sections"), "attrs=%s", e.Attributes)

	// re-import of the same key with different sections: the page has a doc now
	res, err = svc.Import(ctx, &testUserID, batch("k", `{"title":"T","page_sections":`+rawSecs("z")+`}`))
	testutil.RequireNoError(t, err)
	testutil.Equal(t, 1, res.Failed)
	testutil.True(t, errors.Is(res.Items[0].Err, ErrPageSectionsRemoved), "err=%v", res.Items[0].Err)

	// Invalid doc on import fails that item.
	res, _ = svc.Import(ctx, &testUserID, batch("z", `{"title":"T","page_doc":{"v":9}}`))
	testutil.True(t, errors.Is(res.Items[0].Err, ErrInvalidDoc), "err=%v", res.Items[0].Err)
}

func TestService_PageDoc_NameOnlyUpdateConverts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, sr, er := newTestService()
	schema := seedSchema(t, sr)
	stored := attrsWith(`"page_sections":` + rawSecs("one"))
	e := seedEntity(t, er, schema, func(e *Entity) { e.Attributes = stored })
	name := "Renamed"
	got, err := svc.UpdateEntity(ctx, e.ID, &testUserID, &UpdateEntityInput{Name: &name})
	testutil.RequireNoError(t, err)
	var m map[string]json.RawMessage
	testutil.RequireNoError(t, json.Unmarshal(got.Attributes, &m))
	testutil.True(t, strings.Contains(string(m["page_doc"]), "one"), "attrs=%s", got.Attributes)
	testutil.True(t, jsonRawEqual(m["page_sections"], json.RawMessage(rawSecs("one"))), "mirror not frozen: %s", m["page_sections"])

	// a page whose sections do not convert is written as it is
	bad := seedEntity(t, er, schema, func(e *Entity) {
		e.Slug = "bad"
		e.Attributes = attrsWith(`"page_sections":[{"id":"d","type":"content"},{"id":"d","type":"content"}]`)
	})
	got, err = svc.UpdateEntity(ctx, bad.ID, &testUserID, &UpdateEntityInput{Name: &name})
	testutil.RequireNoError(t, err)
	testutil.True(t, !hasKey(t, got.Attributes, "page_doc"), "attrs=%s", got.Attributes)
}

func TestService_PageDoc_HookEdits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	seed := func(t *testing.T) (*Service, *Entity) {
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		e := seedEntity(t, er, schema, func(e *Entity) {
			e.Attributes = attrsWith(`"page_sections":`+rawSecs("one"), `"page_doc":`+v1Doc(t, "one"))
		})
		return svc, e
	}
	edit := func(f func(m map[string]any)) BeforeSaveFunc {
		return func(_ context.Context, in *BeforeSaveInput) error {
			var m map[string]any
			testutil.RequireNoError(t, json.Unmarshal(in.Attributes, &m))
			f(m)
			b, err := json.Marshal(m)
			in.Attributes = b
			return err
		}
	}
	save := func(svc *Service, e *Entity) (*Entity, error) {
		in := append(json.RawMessage(nil), e.Attributes...)
		return svc.UpdateEntity(ctx, e.ID, &testUserID, &UpdateEntityInput{Attributes: &in})
	}

	t.Run("a changed page_sections is discarded, the save goes through", func(t *testing.T) {
		t.Parallel()
		svc, e := seed(t)
		svc.SetBeforeSave(edit(func(m map[string]any) {
			m["page_sections"] = append(m["page_sections"].([]any), map[string]any{"id": "h", "type": "content", "sort_order": 1, "content": map[string]any{"body": "HOOKED"}})
			m["title"] = "from hook"
		}))
		got, err := save(svc, e)
		testutil.RequireNoError(t, err)
		testutil.True(t, !strings.Contains(string(got.Attributes), "HOOKED") && strings.Contains(string(got.Attributes), "from hook"), "attrs=%s", got.Attributes)
	})

	t.Run("a changed page_doc is kept and validated", func(t *testing.T) {
		t.Parallel()
		svc, e := seed(t)
		var doc map[string]any
		testutil.RequireNoError(t, json.Unmarshal([]byte(twoCol), &doc))
		svc.SetBeforeSave(edit(func(m map[string]any) { m["page_doc"] = doc }))
		got, err := save(svc, e)
		testutil.RequireNoError(t, err)
		testutil.True(t, strings.Contains(string(got.Attributes), `"heading"`), "attrs=%s", got.Attributes)

		svc.SetBeforeSave(edit(func(m map[string]any) { m["page_doc"] = map[string]any{"v": 9} }))
		_, err = save(svc, e)
		testutil.True(t, errors.Is(err, ErrInvalidDoc), "err=%v", err)
	})

	t.Run("post types keep page_sections as plain data", func(t *testing.T) {
		t.Parallel()
		svc, sr, er := newTestService()
		schema := seedSchema(t, sr)
		schema.Slug = "post"
		e := seedEntity(t, er, schema, func(e *Entity) { e.Attributes = attrsWith(`"page_sections":` + rawSecs("one")) })
		svc.SetBeforeSave(edit(func(m map[string]any) { m["page_sections"] = []any{} }))
		got, err := save(svc, e)
		testutil.RequireNoError(t, err)
		testutil.True(t, strings.Contains(string(got.Attributes), `"page_sections":[]`) && !hasKey(t, got.Attributes, "page_doc"), "attrs=%s", got.Attributes)
	})
}
