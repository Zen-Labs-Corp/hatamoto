package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

// Bad slugs, parents and authors are the caller's mistake: a 4xx with a stable
// code, never a 500.
func TestWriteEntityRefError(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{entity.ErrSlugAlreadyExists, 409, "slug_exists"},
		{fmt.Errorf("%w: same parent", entity.ErrSlugAlreadyExists), 409, "slug_exists"},
		{entity.ErrHasChildren, 409, "has_children"},
		{entity.ErrParentNotFound, 422, "parent_not_found"},
		{entity.ErrParentCycle, 422, "parent_cycle"},
		{entity.ErrParentTooDeep, 422, "parent_too_deep"},
		{entity.ErrAuthorNotFound, 422, "author_not_found"},
	} {
		rec := httptest.NewRecorder()
		if !writeEntityRefError(rec, tc.err) {
			t.Errorf("%v not handled", tc.err)
			continue
		}
		var body map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != tc.status || body["code"] != tc.code || body["error"] != tc.err.Error() {
			t.Errorf("%v -> %d %v", tc.err, rec.Code, body)
		}
	}
	if writeEntityRefError(httptest.NewRecorder(), http.ErrAbortHandler) {
		t.Error("an unrelated error must fall through")
	}
}
