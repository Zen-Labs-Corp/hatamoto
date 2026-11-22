package server

import (
	"context"
	"testing"
	"time"

	"hatamoto.dev/core/internal/llmstxt"
)

func TestAIFilesStatusOf(t *testing.T) {
	at := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	st := llmstxt.State{LLMsChangedAt: &at, LLMsBytes: 1500, FullBytes: 0}
	for _, txt := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		st.Changes = append(st.Changes, llmstxt.Change{At: at, Text: txt})
	}
	got := aiFilesStatusOf(st)
	if got.LLMs.ChangedAt == nil || !got.LLMs.ChangedAt.Equal(at) || got.LLMs.Bytes != 1500 {
		t.Errorf("llms = %+v", got.LLMs)
	}
	if got.Full.ChangedAt != nil || got.Full.Bytes != 0 {
		t.Errorf("full = %+v, want the never-built zero value", got.Full)
	}
	if len(got.Changes) != 5 || got.Changes[0].Text != "a" || got.Changes[4].Text != "e" {
		t.Errorf("changes = %+v, want the newest five", got.Changes)
	}
	if empty := aiFilesStatusOf(llmstxt.State{}); len(empty.Changes) != 0 {
		t.Errorf("empty state has changes: %+v", empty.Changes)
	}
}

func TestAIFilesStatusNilStore(t *testing.T) {
	st, err := (&Server{}).aiFilesStatus(context.Background())
	if err != nil || st.LLMs.ChangedAt != nil || len(st.Changes) != 0 {
		t.Fatalf("nil store: %+v, %v", st, err)
	}
}
