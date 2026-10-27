package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

// refRepo serves GetByID from a map; every other repository method panics
// (nil embedded interface), so the adapter provably reads nothing else.
type refRepo struct {
	entity.EntityRepository
	byID map[uuid.UUID]*entity.Entity
	err  error
}

func (r refRepo) GetByID(_ context.Context, id uuid.UUID) (*entity.Entity, error) {
	if r.err != nil {
		return nil, r.err
	}
	if e, ok := r.byID[id]; ok {
		return e, nil
	}
	return nil, entity.ErrEntityNotFound
}

func refEntity(schema, attrs string) *entity.Entity {
	return &entity.Entity{ID: uuid.New(), Schema: &entity.EntitySchema{Slug: schema, IsBuiltin: true}, Status: entity.StatusDraft, Attributes: json.RawMessage(attrs)}
}

const refTestDoc = `{"v":2,"nodes":[{"id":"t","t":"text","p":{"text":"synced words"}}]}`

func newRefData(repo refRepo) *renderData {
	srv := &Server{entityRepo: repo, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return newRenderData(srv)
}

func TestRenderDataRefSource(t *testing.T) {
	block := refEntity("block", `{"page_doc":`+refTestDoc+`}`)
	empty := refEntity("block", `{}`)
	page := refEntity("page", `{"page_doc":`+refTestDoc+`}`)
	header := refEntity("theme_part", `{"slot":"header","page_doc":`+refTestDoc+`}`)
	broken := refEntity("block", `{"page_doc":{"v":9}}`)
	repo := refRepo{byID: map[uuid.UUID]*entity.Entity{}}
	for _, e := range []*entity.Entity{block, empty, page, header, broken} {
		repo.byID[e.ID] = e
	}
	d := newRefData(repo)
	ctx := context.Background()

	if doc, ok := d.Block(ctx, block.ID.String()); !ok || len(doc.Nodes) != 1 {
		t.Errorf("block (a draft) = %v %v", doc, ok)
	}
	if doc, ok := d.Block(ctx, empty.ID.String()); !ok || len(doc.Nodes) != 0 {
		t.Errorf("a block without a doc is empty, got %v %v", doc, ok)
	}
	for name, id := range map[string]string{
		"page":      page.ID.String(),
		"part":      header.ID.String(),
		"broken":    broken.ID.String(),
		"unknown":   uuid.NewString(),
		"not-a-uid": "nope",
	} {
		if _, ok := d.Block(ctx, id); ok {
			t.Errorf("Block(%s) must be missing", name)
		}
	}
	if _, ok := d.Part(ctx, "header", header.ID.String()); !ok {
		t.Error("header part not found")
	}
	if _, ok := d.Part(ctx, "footer", header.ID.String()); ok {
		t.Error("a part of another slot must be missing")
	}
	if _, ok := d.Part(ctx, "header", block.ID.String()); ok {
		t.Error("a block is not a part")
	}
	if d.Failures() != 0 {
		t.Errorf("not-found must not count as a failure, got %d", d.Failures())
	}

	failing := newRefData(refRepo{err: errors.New("db down")})
	if _, ok := failing.Block(ctx, block.ID.String()); ok || failing.Failures() != 1 {
		t.Errorf("a failed lookup must count (failures=%d)", failing.Failures())
	}
	var nilData *renderData
	if _, ok := nilData.Block(ctx, block.ID.String()); ok {
		t.Error("nil renderData must be missing, not panic")
	}
}

// The adapter feeds the engine: the same block renders in the public page and
// in a canvas patch.
func TestRenderDataRefsRenderThroughEngine(t *testing.T) {
	block := refEntity("block", `{"page_doc":`+refTestDoc+`}`)
	d := newRefData(refRepo{byID: map[uuid.UUID]*entity.Entity{block.ID: block}})
	e, err := render.New(render.Options{Refs: d})
	if err != nil {
		t.Fatal(err)
	}
	ref := &builder.Node{ID: "r1", T: "ref", P: map[string]any{"block": block.ID.String()}}
	for _, canvas := range []bool{false, true} {
		var b bytes.Buffer
		if err := e.RenderNode(&b, &render.PageView{}, ref, canvas); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), "synced words") {
			t.Errorf("canvas=%v: %s", canvas, b.String())
		}
	}
}
