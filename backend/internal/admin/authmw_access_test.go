package admin

import (
	"net/http"
	"testing"
	"time"
)

// A live cookie of a user whose access has ended is sent to the sign-in page
// on the next request; moving the end into the future lets it through again.
func TestAccessEndedCookieRefused(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	if rec := g.do("GET", "/admin/profile", "owner-token", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("open user: %d", rec.Code)
	}
	past := time.Now().Add(-time.Minute)
	g.owner.AccessEndsAt = &past
	if rec := g.do("GET", "/admin/profile", "owner-token", nil, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("ended user: %d, want a redirect to sign in", rec.Code)
	}
	future := time.Now().Add(time.Hour)
	g.owner.AccessEndsAt = &future
	if rec := g.do("GET", "/admin/profile", "owner-token", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("extended user: %d", rec.Code)
	}
}
