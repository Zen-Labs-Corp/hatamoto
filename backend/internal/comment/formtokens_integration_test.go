//go:build integration

package comment

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFormTokenGuards(t *testing.T) {
	f := newFixture(t)
	for _, id := range []string{"", strings.Repeat("a", MaxFormTokenID+1)} {
		if _, err := f.svc.ConsumeFormToken(f.ctx, id, time.Now().Add(time.Hour)); !errors.Is(err, ErrFormTokenID) {
			t.Errorf("len %d: err = %v, want ErrFormTokenID", len(id), err)
		}
	}
	if fresh, err := f.svc.ConsumeFormToken(f.ctx, "expired", time.Now().Add(-time.Minute)); err != nil || fresh {
		t.Errorf("expired token: fresh = %v, %v; want false, nil", fresh, err)
	}
	var n int
	f.scalar(&n, `SELECT count(*)::int FROM hatamoto_comment_form_tokens`)
	if n != 0 {
		t.Errorf("rows after rejected tokens = %d, want 0", n)
	}
	if fresh, err := f.svc.ConsumeFormToken(f.ctx, strings.Repeat("a", MaxFormTokenID), time.Now().Add(time.Hour)); err != nil || !fresh {
		t.Errorf("max-length token: fresh = %v, %v", fresh, err)
	}
}

func TestPurgeFormTokens(t *testing.T) {
	f := newFixture(t)
	f.exec(`INSERT INTO hatamoto_comment_form_tokens (token_id, expires_at, used_at) VALUES ('old', now() - interval '1 hour', now() - interval '2 hours')`)
	if fresh, err := f.svc.ConsumeFormToken(f.ctx, "live", time.Now().Add(time.Hour)); err != nil || !fresh {
		t.Fatalf("live token: fresh = %v, %v", fresh, err)
	}
	if n, err := f.svc.PurgeFormTokens(f.ctx); err != nil || n != 1 {
		t.Fatalf("purge = %d, %v; want 1", n, err)
	}
	var left string
	f.scalar(&left, `SELECT string_agg(token_id, ',') FROM hatamoto_comment_form_tokens`)
	if left != "live" {
		t.Errorf("remaining = %q, want live", left)
	}
	if fresh, _ := f.svc.ConsumeFormToken(f.ctx, "live", time.Now().Add(time.Hour)); fresh {
		t.Error("live token replayable")
	}
}
