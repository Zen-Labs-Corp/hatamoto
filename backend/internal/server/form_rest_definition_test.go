package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/jsonx"
)

// An invalid definition is the caller's mistake: 422 with the field list, not
// a 500 and an error log.
func TestFormRESTWritesRejectInvalidDefinitionWith422(t *testing.T) {
	t.Parallel()
	srv, _ := newTypesServer(t, &form.Form{})
	cases := []struct {
		name, body, field string
	}{
		{"self-referencing logic", `{"name":"F","fields":[{"id":"a","type":"text","name":"a","label":"A","logic":{"field_id":"a","op":"is","value":"x"}}]}`, "a"},
		{"heading without text", `{"name":"F","fields":[{"id":"h","type":"heading"}]}`, "h"},
		{"hidden field without a source", `{"name":"F","fields":[{"id":"k","type":"hidden","name":"k"}]}`, "k"},
		{"javascript redirect", `{"name":"F","fields":[],"settings":{"success_redirect_url":"javascript:alert(1)"}}`, "settings.success_redirect_url"},
	}
	for _, c := range cases {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			req := httptest.NewRequest(method, "/api/v1/forms", strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			if method == http.MethodPost {
				srv.handleCreateForm(rec, req)
			} else {
				req.SetPathValue("id", uuid.NewString())
				srv.handleUpdateForm(rec, req)
			}
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s %s: status %d, want 422: %s", method, c.name, rec.Code, rec.Body.String())
				continue
			}
			var out struct {
				Errors []struct {
					FieldID string `json:"field_id"`
					Message string `json:"message"`
				} `json:"errors"`
			}
			if err := jsonx.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Errors) == 0 || out.Errors[0].FieldID != c.field || out.Errors[0].Message == "" {
				t.Errorf("%s %s: body %s", method, c.name, rec.Body.String())
			}
		}
	}
}
