package pluginhost

import (
	"fmt"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

func TestIsInvalid_PageDocErrors(t *testing.T) {
	for _, err := range []error{fmt.Errorf("%w: bad", entity.ErrInvalidDoc), entity.ErrPageSectionsRemoved} {
		if !isInvalid(err) {
			t.Errorf("isInvalid(%v) = false: a plugin would see an internal error", err)
		}
	}
}
