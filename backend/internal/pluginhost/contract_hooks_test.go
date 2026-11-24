package pluginhost

import (
	"testing"

	"hatamoto.dev/sdk"
)

func TestContractHooks(t *testing.T) {
	for _, h := range []string{"comment.updated", "comment.created", "comment.approved", "comment.spam", "comment.deleted"} {
		if !isCommentEvent(h) {
			t.Errorf("isCommentEvent(%q) = false", h)
		}
	}
	if isCommentEvent("entity.created") {
		t.Error("isCommentEvent(entity.created) = true")
	}
	for hook, want := range map[string]sdk.Permission{
		"comment.draft":      sdk.PermCommentsRead,
		"comment.check":      sdk.PermCommentsRead,
		"entity.before_save": sdk.PermEntitiesFilter,
		"render.page":        "",
	} {
		if got := filterGrant(hook); got != want {
			t.Errorf("filterGrant(%q) = %q, want %q", hook, got, want)
		}
	}
}
