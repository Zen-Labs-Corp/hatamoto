package pluginhost

import (
	"context"
	"errors"

	"encoding/json/jsontext"

	"hatamoto.dev/sdk"
)

// CommentIdentity serves comments.identity.ensure/revoke; nil = unavailable.
// The implementation derives the commenter key and audits the call.
type CommentIdentity interface {
	Ensure(ctx context.Context, plugin, account string) (commenterKey string, err error)
	Revoke(ctx context.Context, plugin, account string) (revoked bool, err error)
}

// ErrCommentIdentityUnavailable means there is no server secret or comments
// are not built yet; the plugin sees "unavailable".
var ErrCommentIdentityUnavailable = errors.New("pluginhost: commenter identity unavailable")

// identityGate decodes and validates the account, then checks the service and
// takes one token from the shared writes limiter.
func identityGate(a *API, e *callEnv, raw jsontext.Value, fn string) (string, error) {
	args, err := decodeArgs[sdk.CommentsIdentityArgs](raw)
	if err != nil {
		return "", err
	}
	if !sdk.ValidIdentityAccount(args.Account) {
		return "", invalid("account: 1 to %d bytes of text", sdk.MaxIdentityAccountBytes)
	}
	if a.svc.CommentIdentity == nil {
		return "", unavailable("comments.identity")
	}
	if err := a.allowWrite(e.plugin, fn); err != nil {
		return "", err
	}
	return args.Account, nil
}

func fnCommentsIdentityEnsure(ctx context.Context, a *API, e *callEnv, raw jsontext.Value) (any, error) {
	account, err := identityGate(a, e, raw, sdk.FnCommentsIdentityEnsure)
	if err != nil {
		return nil, err
	}
	key, err := a.svc.CommentIdentity.Ensure(ctx, e.plugin, account)
	if err != nil {
		return nil, err
	}
	return sdk.CommentsIdentityEnsured{CommenterKey: key}, nil
}

func fnCommentsIdentityRevoke(ctx context.Context, a *API, e *callEnv, raw jsontext.Value) (any, error) {
	account, err := identityGate(a, e, raw, sdk.FnCommentsIdentityRevoke)
	if err != nil {
		return nil, err
	}
	ok, err := a.svc.CommentIdentity.Revoke(ctx, e.plugin, account)
	if err != nil {
		return nil, err
	}
	return sdk.CommentsIdentityRevoked{Revoked: ok}, nil
}
