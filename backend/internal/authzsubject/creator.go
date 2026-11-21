package authzsubject

import (
	"context"
	"log/slog"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/auth"
)

// CreatorCanAct reports whether the key's creator may still let it act
// (apikey.creator.act, asked as the creator's session). A key without a
// creator, a deny and an authorizer error all answer false.
func CreatorCanAct(ctx context.Context, k *auth.APIKey) bool {
	if k == nil || k.CreatedBy == nil {
		return false
	}
	ok, err := authz.Can(ctx, ForSession(k.CreatedBy.String(), k.CreatorRole), authz.ActionAPIKeyCreatorAct, authz.Object{})
	if err != nil {
		slog.WarnContext(ctx, "authz check failed", "action", string(authz.ActionAPIKeyCreatorAct), "err", err)
		return false
	}
	return ok
}
