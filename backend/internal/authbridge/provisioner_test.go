package authbridge

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/internal/authstate"
)

// An empty email is a validation error, not "not found": a module author
// looks at the input, not at a missing user.
func TestCreateEmptyEmailIsInvalidEmail(t *testing.T) {
	t.Parallel()

	p := &Provisioner{}
	for _, email := range []string{"", "   "} {
		_, err := p.Create(context.Background(), authstate.NewUser{Email: email, Role: "editor"})
		if !errors.Is(err, authstate.ErrInvalidEmail) {
			t.Errorf("Create(%q) = %v, want ErrInvalidEmail", email, err)
		}
	}
}
