package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

// An invalid page_doc is 422 invalid_page_doc and a page_sections write
// that changes a page with a doc is 422 page_sections_removed. Neither is a 400: the create,
// update and restore handlers test ErrValidationFailed (400) and must not see it.
func TestWriteEntityRefError_PageDoc(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{entity.ErrInvalidDoc, 422, "invalid_page_doc"},
		{fmt.Errorf("%w: v must be 2", entity.ErrInvalidDoc), 422, "invalid_page_doc"},
		{fmt.Errorf("cannot restore: %w", entity.ErrInvalidDoc), 422, "invalid_page_doc"},
		{entity.ErrPageSectionsRemoved, 422, "page_sections_removed"},
	} {
		if errors.Is(tc.err, entity.ErrValidationFailed) {
			t.Errorf("%v wraps ErrValidationFailed (would map to 400)", tc.err)
		}
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
}
