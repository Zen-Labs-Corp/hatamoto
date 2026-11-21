package admin

import (
	"net/url"
	"strings"
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
	evs := read()
	ev := loginAuditOne(t, evs, AuditSignInFailed)
	if ev.Result != audit.ResultDenied || ev.Actor != "unknown" || ev.Detail["reason"] != ReasonCredentials || ev.Detail["method"] != SignInPasskey {
		t.Fatalf("row = %+v", ev)
	}
	assertAuditSafe(t, evs)
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

func passkeyAuditCallback(t *testing.T, g *providerRig) {
	t.Helper()
	c, state := g.start(t, "/admin/login/provider/example/start")
	g.nav(cbPath+"?state="+url.QueryEscape(state), c)
}

func TestPasskeyAuditProviderSignIn(t *testing.T) {
	t.Parallel()

	g := newProviderRig(t)
	g.mfa = true
	read := auditCapture(g.a)
	passkeyAuditCallback(t, g)
	evs := read()
	ev := loginAuditOne(t, evs, AuditSignIn)
	if ev.Detail["method"] != SignInProvider || ev.Detail["provider"] != "example" || ev.ActorID != g.uid.String() || ev.Actor != "olga@example.com" {
		t.Fatalf("row = %+v", ev)
	}
	assertAuditSafe(t, evs)
}

func TestPasskeyAuditProviderRefused(t *testing.T) {
	t.Parallel()

	g := newProviderRig(t)
	g.auth.verifiedErr = auth.ErrAccountLocked
	read := auditCapture(g.a)
	passkeyAuditCallback(t, g)
	evs := read()
	ev := loginAuditOne(t, evs, AuditSignInFailed)
	if ev.Detail["reason"] != ReasonLocked || ev.Detail["provider"] != "example" || ev.Result != audit.ResultDenied {
		t.Fatalf("row = %+v", ev)
	}
	if strings.Contains(strings.ToLower(ev.Actor), "@") {
		t.Fatalf("actor = %q", ev.Actor)
	}
	assertAuditSafe(t, evs)
}

func TestPasskeyAuditProviderFinishError(t *testing.T) {
	t.Parallel()

	g := newProviderRig(t)
	g.finErr = auth.ErrInvalidCredentials
	read := auditCapture(g.a)
	passkeyAuditCallback(t, g)
	evs := read()
	ev := loginAuditOne(t, evs, AuditSignInFailed)
	if ev.Actor != "unknown" || ev.Detail["reason"] != ReasonCredentials {
		t.Fatalf("row = %+v", ev)
	}
	assertAuditSafe(t, evs)
}

func TestPasskeyAuditProviderPendingSecondStepWritesNothing(t *testing.T) {
	t.Parallel()

	g := newProviderRig(t)
	read := auditCapture(g.a)
	passkeyAuditCallback(t, g)
	if evs := read(); len(evs) != 0 {
		t.Fatalf("pending second step wrote %+v", evs)
	}
}
