package mcpserver

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	hmauth "hatamoto.dev/core/internal/auth"
)

func TestReorderToolsNeedTheirWriteScope(t *testing.T) {
	for tool, scope := range map[string]hmauth.APIKeyScope{
		"entities_reorder": hmauth.ScopeReadEntities,
		"terms_reorder":    hmauth.ScopeReadEntities,
	} {
		text, isErr := callSEO(t, []hmauth.APIKeyScope{scope}, Deps{}, tool, map[string]any{"id": uuid.NewString(), "position": 1})
		if !isErr || strings.TrimSpace(text) == "" {
			t.Errorf("%s with a read-only key = %v %q, want a refusal", tool, isErr, text)
		}
	}
	// A terms write key may not reorder entities, and the reverse.
	if _, isErr := callSEO(t, []hmauth.APIKeyScope{hmauth.ScopeWriteTerms}, Deps{}, "entities_reorder", map[string]any{"id": uuid.NewString(), "position": 1}); !isErr {
		t.Error("entities_reorder accepted a terms-only key")
	}
	if _, isErr := callSEO(t, []hmauth.APIKeyScope{hmauth.ScopeWriteEntities}, Deps{}, "terms_reorder", map[string]any{"id": uuid.NewString(), "position": 1}); !isErr {
		t.Error("terms_reorder accepted an entities-only key")
	}
}

func TestReorderToolsRejectBadIDs(t *testing.T) {
	text, isErr := callSEO(t, []hmauth.APIKeyScope{hmauth.ScopeWriteEntities}, Deps{}, "entities_reorder", map[string]any{"id": "nope", "position": 1})
	if !isErr || !strings.Contains(text, "id") {
		t.Errorf("bad id = %v %q", isErr, text)
	}
}
