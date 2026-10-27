package app

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/adminpath"
)

// `hatamoto admin-url set` refuses the first segment of PUBLIC_API_PREFIX. The
// nil pool proves the answer comes before any database lookup.
func TestSQLSiteServesAPIPrefixSegment(t *testing.T) {
	s := &sqlSite{apiPrefix: "/studio/v1"}
	if ok, _ := s.Serves(context.Background(), "/studio"); !ok {
		t.Fatal("/studio must be reserved while PUBLIC_API_PREFIX is /studio/v1")
	}
	if res := adminpath.Assess(context.Background(), "/studio", s); res.Level != adminpath.LevelReserved {
		t.Fatalf("assessed %v, want reserved", res.Level)
	}
}
