package server

import (
	"context"
	"testing"
)

// A crawler fetching /fr/about is attributed to the French page, the same way
// the public loader serves it; the unprefixed path of that page is a miss.
func TestLookupAgentEntity_Locales(t *testing.T) {
	f := newLocaleRouteFixture(t, false, fixtureLocales...)
	ctx := context.Background()
	snap, err := f.deps.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path      string
		wantFound bool
		wantID    bool
	}{
		{"/fr/about", true, true},
		{"/welcome", true, true},
		{"/about", false, false}, // French page, default-locale scope
		{"/fr", true, false},     // locale home: served, no entity
		{"/fr/missing", false, false},
	}
	for _, tt := range tests {
		id, found, err := lookupAgentEntity(ctx, f.deps.repo, snap, tt.path)
		if err != nil || found != tt.wantFound || (id != nil) != tt.wantID {
			t.Errorf("%s: id=%v found=%v err=%v, want found=%v entity=%v", tt.path, id, found, err, tt.wantFound, tt.wantID)
		}
	}
}
