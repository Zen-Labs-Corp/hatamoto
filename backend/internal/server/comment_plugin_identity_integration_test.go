//go:build integration

package server

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/pluginhost"
)

func identityAudits(t *testing.T, s *Server, fn string) int {
	t.Helper()
	var n int
	if err := s.db.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM hatamoto_admin_audit WHERE action='plugin.comment.identity' AND detail->>'fn'=$1`, fn).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPluginCommentIdentity(t *testing.T) {
	ctx := context.Background()
	rig := newModerationRig(t)
	derive := func(plugin string, id uuid.UUID) string { return comment.PluginCommenterKey("k", plugin, id) }
	ident := pluginCommentIdentity{s: rig.s, derive: derive}

	k1, err := ident.Ensure(ctx, "alpha", "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	k2, err := ident.Ensure(ctx, "alpha", "acct-1")
	if err != nil || k1 != k2 || k1 == "" {
		t.Fatalf("Ensure twice: %q %q %v", k1, k2, err)
	}
	if n := identityAudits(t, rig.s, "ensure"); n != 1 {
		t.Fatalf("ensure audit rows = %d, want 1", n)
	}
	k3, err := ident.Ensure(ctx, "beta", "acct-1")
	if err != nil || k3 == k1 {
		t.Fatalf("other plugin key = %q (same as %q), err %v", k3, k1, err)
	}

	id, ok, err := rig.s.comments.PluginCommenter(ctx, "alpha", "acct-1")
	if err != nil || !ok {
		t.Fatalf("PluginCommenter: %v %v", ok, err)
	}
	tv := func() int {
		var v int
		if err := rig.s.db.Pool().QueryRow(ctx, `SELECT token_version FROM hatamoto_commenters WHERE id=$1`, id).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := tv()
	if got, err := ident.Revoke(ctx, "alpha", "acct-1"); err != nil || !got {
		t.Fatalf("Revoke known = %v, %v", got, err)
	}
	if tv() != before+1 {
		t.Fatalf("token_version = %d, want %d", tv(), before+1)
	}
	if n := identityAudits(t, rig.s, "revoke"); n != 1 {
		t.Fatalf("revoke audit rows = %d, want 1", n)
	}
	if got, err := ident.Revoke(ctx, "alpha", "nobody"); err != nil || got {
		t.Fatalf("Revoke unknown = %v, %v", got, err)
	}
	if n := identityAudits(t, rig.s, "revoke"); n != 1 {
		t.Fatalf("unknown revoke audited: %d rows", n)
	}

	rig.s.comments = nil
	if _, err := ident.Ensure(ctx, "alpha", "acct-1"); !errors.Is(err, pluginhost.ErrCommentIdentityUnavailable) {
		t.Fatalf("Ensure without comments: %v", err)
	}
	if _, err := ident.Revoke(ctx, "alpha", "acct-1"); !errors.Is(err, pluginhost.ErrCommentIdentityUnavailable) {
		t.Fatalf("Revoke without comments: %v", err)
	}
}
