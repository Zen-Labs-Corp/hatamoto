package server

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"hatamoto.dev/core/internal/agentpolicystate"
	"hatamoto.dev/core/internal/auditlog"
)

func TestAgentPolicyWiringNoDatabaseLeavesRuntimeUnset(t *testing.T) {
	agentpolicystate.Reset()
	t.Cleanup(agentpolicystate.Reset)
	(&Server{}).wireAgentPolicy()
	_, err := agentpolicystate.Changes(context.Background(), "6f1c0c0e-5d0a-4a43-9a52-0d3f4c1f9e11", time.Time{})
	if !errors.Is(err, agentpolicystate.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestAgentPolicyWiringMapKeyChangesCopiesEveryField(t *testing.T) {
	at := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	in := []auditlog.KeyChange{{
		ID: 7, At: at, Actor: "api-key:ci", Action: "entity.update", Target: "t1",
		Result: "ok", Via: "mcp", Detail: map[string]any{"k": "v"},
	}}
	want := []agentpolicystate.Change{{
		ID: 7, At: at, Action: "entity.update", Target: "t1", Result: "ok",
		Via: "mcp", Actor: "api-key:ci", Detail: map[string]any{"k": "v"},
	}}
	if got := mapKeyChanges(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got := mapKeyChanges(nil); len(got) != 0 {
		t.Fatalf("nil rows -> %v", got)
	}
}
