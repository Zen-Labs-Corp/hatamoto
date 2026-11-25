package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Every REST list refuses a paging parameter it cannot honour, and the other
// spelling of its page size, before reading anything: none falls back to a
// default without a word (#385, media reported in #231).
func TestListEndpointsRefuseBadPaging(t *testing.T) {
	t.Parallel()
	s := &Server{logger: slog.Default()}
	const id = "11111111-1111-1111-1111-111111111111"
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		query   string
		naming  string
	}{
		{"media limit", s.handleMediaList, "limit=abc", "limit"},
		{"media offset", s.handleMediaList, "offset=-1", "offset"},
		{"media per_page", s.handleMediaList, "per_page=10", "use limit"},
		{"section templates", s.handleListSectionTemplates, "limit=0", "limit"},
		{"entity versions", s.handleListEntityVersions, "limit=x", "limit"},
		{"form submissions", s.handleListFormSubmissions, "offset=x", "offset"},
		{"form submissions per_page", s.handleListFormSubmissions, "per_page=5", "use limit"},
		{"redirects", s.handleListRedirects, "per_page=0", "per_page"},
		{"redirects page", s.handleListRedirects, "page=x", "page"},
		{"404 log", s.handleList404Entries, "limit=5", "use per_page"},
		{"link rules", s.handleListLinkRules, "page=-2", "page"},
		{"knowledge graph", s.handleKnowledgeGraph, "per_page=abc", "per_page"},
		{"entities", s.handleListEntities, "limit=-1", "limit"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/x?"+tc.query, nil)
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		tc.handler(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.naming) {
			t.Errorf("%s ?%s: %d %s, want 400 naming %q", tc.name, tc.query, rec.Code, rec.Body, tc.naming)
		}
	}
}
