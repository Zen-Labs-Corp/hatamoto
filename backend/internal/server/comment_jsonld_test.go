package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
)

type fakeCounter struct {
	mode     comment.Mode
	modeErr  error
	approved int
}

func (f fakeCounter) EntityMode(context.Context, uuid.UUID) (comment.Mode, error) {
	return f.mode, f.modeErr
}
func (f fakeCounter) Counts(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]comment.EntityCounts, error) {
	return map[uuid.UUID]comment.EntityCounts{ids[0]: {Approved: f.approved}}, nil
}

func articleDoc() map[string]any {
	return map[string]any{"@context": "https://schema.org", "@type": "BlogPosting", "headline": "Hello"}
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCommentJSONLD_CountsApprovedOnly(t *testing.T) {
	id := uuid.New()
	before := jsonOf(t, articleDoc())
	for _, mode := range []comment.Mode{comment.ModeOpen, comment.ModeClosed} {
		doc := addCommentCount(context.Background(), fakeCounter{mode: mode, approved: 3}, id, articleDoc())
		if doc["commentCount"] != 3 {
			t.Fatalf("%s: doc = %v", mode, doc)
		}
		if s := jsonOf(t, doc); strings.Contains(s, `"url"`) || strings.Contains(s, `"Comment"`) || strings.Contains(s, `"author"`) {
			t.Errorf("%s: extra fields in %s", mode, s)
		}
	}
	for name, cc := range map[string]fakeCounter{
		"hidden":  {mode: comment.ModeHidden, approved: 3},
		"off":     {mode: comment.ModeOff, approved: 3},
		"zero":    {mode: comment.ModeOpen},
		"error":   {mode: comment.ModeOpen, modeErr: errors.New("db"), approved: 3},
		"unknown": {modeErr: comment.ErrNotFound, approved: 3},
	} {
		if got := jsonOf(t, addCommentCount(context.Background(), cc, id, articleDoc())); got != before {
			t.Errorf("%s changed the document: %s", name, got)
		}
	}
	if addCommentCount(context.Background(), fakeCounter{mode: comment.ModeOpen, approved: 1}, id, nil) != nil {
		t.Error("nil doc must stay nil")
	}
}

func TestCommentJSONLD_GraphMainNode(t *testing.T) {
	doc := map[string]any{"@context": "https://schema.org", "@graph": []map[string]any{
		{"@type": "BlogPosting", "headline": "Hello"},
		{"@type": "WebPage", "url": "https://x.test/a"},
	}}
	addCommentCount(context.Background(), fakeCounter{mode: comment.ModeOpen, approved: 2}, uuid.New(), doc)
	g := doc["@graph"].([]map[string]any)
	if g[0]["commentCount"] != 2 {
		t.Fatalf("main node = %v", g[0])
	}
	if _, has := g[1]["commentCount"]; has || doc["commentCount"] != nil {
		t.Fatalf("count leaked to another node: %v", doc)
	}
}

func TestCommentJSONLD_NoServiceLeavesDocAlone(t *testing.T) {
	doc := articleDoc()
	if got := (&Server{}).withCommentCount(context.Background(), nil, doc); jsonOf(t, got) != jsonOf(t, articleDoc()) {
		t.Fatal("changed without a comment service")
	}
}
