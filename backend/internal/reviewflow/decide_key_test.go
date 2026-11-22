package reviewflow

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzctx"
)

// A key never decides a staged change, before anything is read.
func TestDecisionsRefuseAKeySubject(t *testing.T) {
	ctx := authzctx.With(context.Background(), authz.Subject{UserID: "creator", Role: "owner", KeyID: testKeyID})
	f := &Flow{} // no engine: a decision that got past the check would panic
	_, errApprove := f.Approve(ctx, 1, "")
	_, errPublish := f.Publish(ctx, 1)
	for name, err := range map[string]error{
		"approve":  errApprove,
		"publish":  errPublish,
		"sendback": f.SendBack(ctx, 1, "no"),
		"withdraw": f.Withdraw(ctx, 1),
		"discard":  f.Discard(ctx, 1),
	} {
		if !errors.Is(err, ErrNotDecider) {
			t.Errorf("%s: err = %v, want ErrNotDecider", name, err)
		}
	}
}
