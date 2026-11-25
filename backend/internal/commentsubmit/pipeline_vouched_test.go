package commentsubmit

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
)

// fakeVouchedPolicy answers with a commenter that has no readable email.
type fakeVouchedPolicy struct{ origin string }

func (f fakeVouchedPolicy) CommenterEmail(_ context.Context, id uuid.UUID) (comment.Commenter, string, error) {
	return comment.Commenter{ID: id, OriginPlugin: f.origin}, "", nil
}

// vouchedHarness is a policy-schema submit by a signed-in commenter a plugin vouches for.
func vouchedHarness(t *testing.T, origin string) *harness {
	t.Helper()
	h := policyHarness(t)
	h.c.commenter = nil // a lookup by email hash must not be needed
	h.p.Policy = fakeVouchedPolicy{origin: origin}
	return h
}

func TestSubmitVouchedCommenterPosts(t *testing.T) {
	h := vouchedHarness(t, "plug")
	h.id.checkErr = identity.ErrEmailInvalid // would refuse any address that reached CheckEmail
	out := h.submit(t)
	if out.Kind != Posted || len(h.c.stored) != 1 {
		t.Fatalf("kind = %d stored = %d, out = %+v", out.Kind, len(h.c.stored), out)
	}
	d := h.c.stored[0]
	if d.CommenterID == nil || *d.CommenterID != *h.req.DeviceCommenter || d.Email != "" || d.AuthorName != "Grace H." {
		t.Errorf("draft = commenter %v, email %q, name %q", d.CommenterID, d.Email, d.AuthorName)
	}
	if h.id.confirms != 0 {
		t.Errorf("RequestConfirmation called %d times", h.id.confirms)
	}
}

func TestSubmitVouchedLimitsByCommenterID(t *testing.T) {
	h := vouchedHarness(t, "plug")
	first, second := uuid.New(), uuid.New()
	post := func(i int, who uuid.UUID) {
		t.Helper()
		h.req.IP = fmt.Sprintf("203.0.%d.7", 113+i) // a new address and network each time
		h.fresh(t0.Add(-time.Minute))
		h.req.DeviceCommenter = &who
		h.submit(t)
	}
	for i := 0; i < 5; i++ {
		post(i, first)
	}
	if len(h.c.stored) != 3 {
		t.Fatalf("stored %d posts by one commenter, want 3 (the per-commenter burst)", len(h.c.stored))
	}
	post(5, second)
	if len(h.c.stored) != 4 {
		t.Errorf("stored = %d, a second commenter must not be limited", len(h.c.stored))
	}
}

func TestSubmitNoEmailNoOriginStillSignIn(t *testing.T) {
	h := vouchedHarness(t, "")
	out := h.submit(t)
	if out.Kind != Invalid || out.Code != CodeSignIn || len(h.c.stored) != 0 {
		t.Fatalf("out = %+v stored = %d", out, len(h.c.stored))
	}
}
