package admin

import (
	"testing"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/internal/auth"
)

func TestPasskeyAuditSignInFirstFactor(t *testing.T) {
	t.Parallel()

	g, _ := newPasskeyRig(t)
	read := auditCapture(g.a)
	if rec := g.passkeySignIn("", `{"id":"ok"}`); rec.Code != 200 {
		t.Fatalf("finish: %d", rec.Code)
	}
	evs := read()
	ev := loginAuditOne(t, evs, AuditSignIn)
	if ev.Detail["method"] != SignInPasskey || ev.Detail["step"] != "first_factor" || ev.ActorID != g.tf.uid.String() || ev.Actor != "olga@example.com" || ev.Result != audit.ResultOK {
		t.Fatalf("row = %+v", ev)
	}
	assertAuditSafe(t, evs)
}

func TestPasskeyAuditSignInSecondFactor(t *testing.T) {
	t.Parallel()

	g, _ := newPasskeyRig(t)
	read := auditCapture(g.a)
	p := g.password(t)
	if evs := read(); len(evs) != 0 {
		t.Fatalf("password with a factor pending wrote %+v", evs)
	}
	if rec := g.passkeySignIn(p, `{"id":"ok"}`); rec.Code != 200 {
		t.Fatalf("finish: %d", rec.Code)
	}
	evs := read()
	ev := loginAuditOne(t, evs, AuditSignIn)
	if ev.Detail["method"] != SignInPasskey || ev.Detail["step"] != "second_factor" {
		t.Fatalf("row = %+v", ev)
	}
	assertAuditSafe(t, evs)
}

func TestPasskeyAuditRefusedAssertion(t *testing.T) {
	t.Parallel()

	g, _ := newPasskeyRig(t)
	read := auditCapture(g.a)
	if rec := g.passkeySignIn("", `{"id":"nouv"}`); rec.Code != 401 {
		t.Fatalf("finish: %d", rec.Code)
	}
	// No user and no typed address: no row.
	if evs := read(); len(evs) != 0 {
		t.Fatalf("rows = %+v", evs)
	}
}

func TestPasskeyAuditRefusedSessionIsLocked(t *testing.T) {
	t.Parallel()

	g, _ := newPasskeyRig(t)
	read := auditCapture(g.a)
	g.auth.verifiedErr = auth.ErrAccountLocked
	if rec := g.passkeySignIn("", `{"id":"ok"}`); rec.Code != 401 {
		t.Fatalf("finish: %d", rec.Code)
	}
	evs := read()
	ev := loginAuditOne(t, evs, AuditSignInFailed)
	if ev.Detail["reason"] != ReasonLocked || ev.ActorID != g.tf.uid.String() {
		t.Fatalf("row = %+v", ev)
	}
	assertAuditSafe(t, evs)
}

// The sign-in hot path is one audit insert: the email comes back from the
// session issue, not from a second user read.
func TestSignInAuditMakesNoExtraUserRead(t *testing.T) {
	t.Parallel()

	t.Run("totp", func(t *testing.T) {
		t.Parallel()
		g := newTwoStepRig(t)
		read := auditCapture(g.a)
		g.second(g.password(t), "123456", nil)
		if ev := loginAuditOne(t, read(), AuditSignIn); ev.Actor != "olga@example.com" {
			t.Fatalf("actor = %q", ev.Actor)
		}
		if n := g.users.reads.Load(); n != 0 {
			t.Fatalf("user reads = %d, want 0", n)
		}
	})
	t.Run("passkey", func(t *testing.T) {
		t.Parallel()
		g, _ := newPasskeyRig(t)
		read := auditCapture(g.a)
		if rec := g.passkeySignIn("", `{"id":"ok"}`); rec.Code != 200 {
			t.Fatalf("finish: %d", rec.Code)
		}
		if ev := loginAuditOne(t, read(), AuditSignIn); ev.Actor != "olga@example.com" {
			t.Fatalf("actor = %q", ev.Actor)
		}
		if n := g.users.reads.Load(); n != 0 {
			t.Fatalf("user reads = %d, want 0", n)
		}
	})
}
