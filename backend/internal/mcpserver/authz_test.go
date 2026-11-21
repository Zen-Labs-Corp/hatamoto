package mcpserver

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hatamoto.dev/core/authz"
	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authztest"
)

// Every tool names an action that the default authorizer answers like the
// tool's scope: allowed with that scope, refused without any.
func TestToolActionsMatchScopes(t *testing.T) {
	s := &Server{mcp: mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)}
	s.registerContent()
	s.registerInsight()
	s.registerBuilder()
	s.registerSEO()
	s.registerComments()
	if len(s.specs) == 0 {
		t.Fatal("no tools registered")
	}
	for _, spec := range s.specs {
		if spec.action == "" {
			t.Errorf("%s has no action", spec.name)
			continue
		}
		with := authz.Subject{KeyID: "k", UserID: "u", CreatorRole: "owner", Scopes: []string{string(spec.scope)}}
		if ok, err := authz.Default().Can(t.Context(), with, spec.action, authz.Object{}); err != nil || !ok {
			t.Errorf("%s: %s with scope %s = %v, %v; want allowed", spec.name, spec.action, spec.scope, ok, err)
		}
		without := with
		without.Scopes = nil
		if ok, err := authz.Default().Can(t.Context(), without, spec.action, authz.Object{}); err != nil || ok {
			t.Errorf("%s: %s without scopes = %v, %v; want refused", spec.name, spec.action, ok, err)
		}
	}
}

// A registered authorizer that refuses everything stops a key that holds every
// scope, and the tool's handler never runs (Deps is empty, so it would panic).
func TestDenyAllAuthorizerRefusesTool(t *testing.T) {
	authztest.Use(t, creatorActAllowed(authztest.DenyAll))
	ts := newTestServer(t, []hmauth.APIKeyScope{hmauth.ScopeAll}, Deps{})
	text, isErr := call(t, session(t, ts), "entities_list", map[string]any{})
	if !isErr || !strings.Contains(text, "forbidden") {
		t.Fatalf("entities_list = %q, isErr %v; want a forbidden error", text, isErr)
	}
}

// An authorizer that fails refuses the call too.
func TestAuthorizerErrorRefusesTool(t *testing.T) {
	authztest.Use(t, creatorActAllowed(authztest.Func(func(_ context.Context, _ authz.Subject, _ authz.Action, _ authz.Object) (bool, error) {
		return true, errors.New("backend down")
	})))
	ts := newTestServer(t, []hmauth.APIKeyScope{hmauth.ScopeAll}, Deps{})
	text, isErr := call(t, session(t, ts), "entities_list", map[string]any{})
	if !isErr || !strings.Contains(text, "forbidden") {
		t.Fatalf("entities_list = %q, isErr %v; want a forbidden error", text, isErr)
	}
}

// The authorizer is asked about the key, not about a session: KeyID is the
// key's, UserID is the key's creator (not the key id the token carries), and
// the scopes are the key's.
func TestAuthorizerSeesKeySubject(t *testing.T) {
	rec := &authztest.Recorder{Answer: authztest.DenyAll}
	authztest.Use(t, creatorActAllowed(rec))
	scopes := []hmauth.APIKeyScope{hmauth.ScopeReadEntities, hmauth.ScopeWriteComments}
	ts := newTestServerRole(t, "editor", scopes, Deps{})
	call(t, session(t, ts), "entities_list", map[string]any{})
	calls := rec.Calls()
	if len(calls) != 1 {
		t.Fatalf("authz asked %d times, want 1", len(calls))
	}
	c := calls[0]
	if c.Action != authz.ActionEntityRead {
		t.Errorf("action = %q, want %q", c.Action, authz.ActionEntityRead)
	}
	sub := c.Subject
	if sub.KeyID != fakeKeyID.String() || sub.UserID != fakeOwner.String() || sub.CreatorRole != "editor" || sub.Role != "" {
		t.Errorf("subject = %+v", sub)
	}
	if !slices.Equal(sub.Scopes, []string{"read:entities", "write:comments"}) {
		t.Errorf("scopes = %v", sub.Scopes)
	}
}
