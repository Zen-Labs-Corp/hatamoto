package server

import (
	"context"
	"encoding/json/jsontext"
	"testing"

	"github.com/google/uuid"
)

func TestApplyPreviewAttributes(t *testing.T) {
	id := uuid.New()
	stored := jsontext.Value(`{"a":1}`)
	ctx := withPreviewAttributes(context.Background(), id, jsontext.Value(`{"a":2}`))

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
