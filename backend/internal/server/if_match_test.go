package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

func TestParseIfMatch(t *testing.T) {
	tests := []struct {
		name    string
		headers []string
		bad     bool
		present bool
		want    *int
	}{
		{name: "absent"},
		{name: "strong", headers: []string{`"9"`}, present: true, want: intp(9)},
		{name: "spaces", headers: []string{` "9" `}, present: true, want: intp(9)},
		{name: "star", headers: []string{`*`}, present: true},
		{name: "weak", headers: []string{`W/"9"`}, bad: true},
		{name: "list", headers: []string{`"9", "10"`}, bad: true},
		{name: "two lines", headers: []string{`"9"`, `"10"`}, bad: true},
		{name: "unquoted", headers: []string{`9`}, bad: true},
		{name: "letters", headers: []string{`"x"`}, bad: true},
		{name: "empty tag", headers: []string{`""`}, bad: true},
		{name: "empty", headers: []string{``}, bad: true},
		{name: "negative", headers: []string{`"-1"`}, bad: true},
		{name: "overflow", headers: []string{`"99999999999999999999999"`}, bad: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPatch, "/", nil)
			for _, h := range tc.headers {
				r.Header.Add("If-Match", h)
			}
			m, err := parseIfMatch(r)
			if tc.bad {
				if err == nil {
					t.Fatalf("want an error, got %+v", m)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if m.present != tc.present {
				t.Fatalf("present = %v", m.present)
			}
			got := m.expected()
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("expected() = %v, want %v", got, tc.want)
			}
		})
	}
}

func intp(n int) *int { return &n }

func TestWriteVersionConflict(t *testing.T) {
	for _, status := range []int{http.StatusPreconditionFailed, http.StatusConflict} {
		rec := httptest.NewRecorder()
		if !writeVersionConflict(rec, &entity.VersionConflictError{Current: 10}, status) {
			t.Fatal("conflict not handled")
		}
		if rec.Code != status || rec.Header().Get("ETag") != `"10"` {
			t.Fatalf("code=%d etag=%q", rec.Code, rec.Header().Get("ETag"))
		}
		var body struct {
			Error   string `json:"error"`
			Current int    `json:"current_version"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Current != 10 || body.Error == "" {
			t.Fatalf("body %s (%v)", rec.Body.String(), err)
		}
	}

	rec := httptest.NewRecorder()
	if writeVersionConflict(rec, errors.New("boom"), http.StatusConflict) {
		t.Fatal("handled a non-conflict")
	}
	if rec.Body.Len() != 0 || rec.Header().Get("ETag") != "" {
		t.Fatal("wrote something for a non-conflict")
	}
}
