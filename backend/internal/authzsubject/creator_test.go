package authzsubject

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authztest"
)

func keyBy(role string) *auth.APIKey {
	id := uuid.New()
	return &auth.APIKey{ID: uuid.New(), CreatedBy: &id, CreatorRole: role}
}

func TestCreatorCanAct(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		key  *auth.APIKey
		want bool
	}{
		{"owner", keyBy("owner"), true},
		{"editor", keyBy("editor"), true},
		{"role gone", keyBy(""), false},
		{"no creator", &auth.APIKey{ID: uuid.New(), CreatorRole: "owner"}, false},
		{"nil key", nil, false},
	} {
		if got := CreatorCanAct(ctx, c.key); got != c.want {
			t.Errorf("%s = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCreatorCanActDenyAll(t *testing.T) {
	authztest.Use(t, authztest.DenyAll)
	if CreatorCanAct(context.Background(), keyBy("owner")) {
		t.Error("owner creator allowed under DenyAll")
	}
}
