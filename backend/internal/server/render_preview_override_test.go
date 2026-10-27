package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestApplyPreviewAttributes(t *testing.T) {
	id := uuid.New()
	stored := json.RawMessage(`{"a":1}`)
	ctx := withPreviewAttributes(context.Background(), id, json.RawMessage(`{"a":2}`))

	got := stored
	applyPreviewAttributes(ctx, id, &got)
	if string(got) != `{"a":2}` {
		t.Errorf("same entity: got %s", got)
	}
	got = stored
	applyPreviewAttributes(ctx, uuid.New(), &got)
	if string(got) != string(stored) {
		t.Errorf("foreign entity was overridden: %s", got)
	}
	got = stored
	applyPreviewAttributes(context.Background(), id, &got)
	if string(got) != string(stored) {
		t.Errorf("no override in ctx changed attrs: %s", got)
	}
}
