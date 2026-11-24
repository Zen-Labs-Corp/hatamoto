package pluginhost

import (
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/sdk"
)

func TestContractStubsUnavailable(t *testing.T) {
	r := newCapaRig(t)
	fns := map[string]string{
		sdk.FnCommentsByAuthor: sdk.PermCommentsRead,
		sdk.FnCommentsSubtree:  sdk.PermCommentsRead,
	}
	perms := []string{sdk.PermEntitiesWriteOwn, sdk.PermCommentsModerate, sdk.PermCommentsRead}
	r.load(t, func(m *sdk.Manifest) { m.Permissions = append(m.Permissions, perms...); m.EntitySchema = "story" })
	for fn, perm := range fns {
		if o := r.do(t, fn, map[string]any{}); o.Code != sdk.ErrCapabilityDenied || o.Cap != perm {
			t.Errorf("%s before grant: %+v", fn, o)
		}
	}
	r.grant(t, perms...)
	for fn := range fns {
		if o := r.do(t, fn, map[string]any{}); o.Code != sdk.ErrUnavailable || o.Msg != fn+" is not available" {
			t.Errorf("%s: %+v, want unavailable", fn, o)
		}
	}
	if o := r.do(t, sdk.FnCommentsThread, map[string]any{"entity_id": uuid.NewString(), "include_dead": true}); o.Code == sdk.ErrInvalidArgument {
		t.Errorf("include_dead refused: %+v", o)
	}
}
