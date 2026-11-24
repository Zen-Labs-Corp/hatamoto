package commentsubmit

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/pluginkey"
	"hatamoto.dev/sdk"
)

func TestHookDrafterUnavailable(t *testing.T) {
	id := uuid.New()
	cases := map[string]*fakeEngine{
		"no filters": {count: 0},
		"plain error": {count: 1, apply: func(context.Context, any) (any, error) {
			return nil, errors.New("boom")
		}},
		"panic": {count: 1, apply: func(context.Context, any) (any, error) { panic("x") }},
		"wrong type": {count: 1, apply: func(context.Context, any) (any, error) {
			return "name", nil
		}},
		"blank": {count: 1, apply: func(_ context.Context, v any) (any, error) {
			d := v.(sdk.CommentDraft)
			d.AuthorName = "  "
			return d, nil
		}},
		"deadline": {count: 1, apply: func(ctx context.Context, _ any) (any, error) {
			return nil, context.DeadlineExceeded
		}},
		"reject without reason": {count: 1, apply: func(context.Context, any) (any, error) {
			return nil, &sdk.Reject{}
		}},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := HookDrafter{Engine: f}.Name(context.Background(), id, "story")
			if !errors.Is(err, ErrDraftUnavailable) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if _, err := (HookDrafter{}).Name(context.Background(), id, "story"); !errors.Is(err, ErrDraftUnavailable) {
		t.Fatalf("nil engine: %v", err)
	}
}

func TestHookDrafterRejected(t *testing.T) {
	f := &fakeEngine{count: 1, apply: func(context.Context, any) (any, error) {
		return nil, &sdk.Reject{Reason: "Pick a name first"}
	}}
	_, err := HookDrafter{Engine: f}.Name(context.Background(), uuid.New(), "story")
	var dr *DraftRejected
	if !errors.As(err, &dr) || dr.Message != "Pick a name first" {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "Pick") {
		t.Fatal("Error() must not carry the message")
	}
}

func TestHookDrafterName(t *testing.T) {
	id := uuid.New()
	var seen uuid.UUID
	f := &fakeEngine{count: 1, apply: func(ctx context.Context, v any) (any, error) {
		seen, _ = pluginkey.CommenterFrom(ctx)
		d := v.(sdk.CommentDraft)
		d.AuthorName = " " + strings.Repeat("é", MaxNameRunes+5) + " "
		return d, nil
	}}
	name, err := HookDrafter{Engine: f}.Name(context.Background(), id, "story")
	if err != nil || name != strings.Repeat("é", MaxNameRunes) {
		t.Fatalf("got %q %v", name, err)
	}
	if f.got != (sdk.CommentDraft{Schema: "story"}) {
		t.Fatalf("engine got %+v", f.got)
	}
	if seen != id {
		t.Fatalf("ctx commenter = %v, want %v", seen, id)
	}
}
