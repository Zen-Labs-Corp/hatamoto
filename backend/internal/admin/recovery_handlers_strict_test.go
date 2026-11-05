package admin

import (
	"net/http"
	"testing"
)

// A recovery write with a duplicate member name or data after the value is a
// 400 and keeps nothing.
func TestRecoveryPutRejectsDuplicateKeysAndTrailingJunk(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	store := newFakeRecovery()
	r.a.deps.Content.Recovery = store
	base := "/admin/entities/" + r.ent.ID.String() + "/edit/recovery"
	for name, body := range map[string]string{
		"duplicate key": `{"content":"a","content":"b","version":1,"name":"T"}`,
		"trailing junk": `{"content":"a","version":1,"name":"T"} junk`,
		"second value":  `{"content":"a","version":1,"name":"T"}{"content":"b"}`,
	} {
		if rec := r.postJSON(base, "owner-token", body, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d %s", name, rec.Code, rec.Body)
		}
	}
	if store.count() != 0 {
		t.Errorf("a refused write kept %d rows", store.count())
	}
}
