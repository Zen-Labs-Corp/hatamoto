package mcpserver

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	hmauth "hatamoto.dev/core/internal/auth"
)

func TestKeyActor(t *testing.T) {
	t.Parallel()
	creator := uuid.New()
	key := &hmauth.APIKey{ID: uuid.New(), Name: "agent", CreatedBy: &creator, KeyHash: "hash-value", KeyPrefix: "hm_pref"}
	a := keyActor(key)
	if a.Kind != "api_key" || a.KeyID != key.ID.String() || a.Name != "api-key:agent" || a.ID != creator.String() || a.Via != "mcp" {
		t.Fatalf("actor = %+v", a)
	}
	if a.IP != "" || a.UserAgent != "" {
		t.Fatalf("MCP has no IP/UA, got %+v", a)
	}
	for _, v := range []string{a.ID, a.KeyID, a.Name, a.Via, a.Kind} {
		if strings.Contains(v, "hash-value") {
			t.Fatalf("actor leaks the key hash: %q", v)
		}
	}
	if a := keyActor(&hmauth.APIKey{ID: uuid.New(), Name: "n"}); a.ID != "" {
		t.Fatalf("creator-less key got actor id %q", a.ID)
	}
}
