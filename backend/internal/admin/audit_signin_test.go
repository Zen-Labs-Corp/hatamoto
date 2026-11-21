package admin

import (
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/audit"
)

func TestSignInAuditHelpers(t *testing.T) {
	t.Parallel()

	a := &Admin{}
	a.deps.HashEmail = func(e string) string { return "email-hash:" + strings.ToUpper(e[:1]) }
	read := auditCapture(a)
	r := httptest.NewRequest("POST", "/admin/login", nil) // no signed-in user in ctx
	r.Header.Set("X-Request-ID", "rid")

	a.auditSignIn(r, "u1", "jane@acme.test", SignInTOTP, map[string]any{"step": "second_factor"})
	a.auditSignInFailed(r, "", a.failedActor("jane@acme.test"), ReasonLocked, nil)
	a.auditSignOut(r, "u1", "jane@acme.test", true)

	evs := read()
	if len(evs) != 3 {
		t.Fatalf("events = %d, want 3", len(evs))
	}
	ok, failed, out := evs[0], evs[1], evs[2]
	if ok.Action != AuditSignIn || ok.Result != audit.ResultOK || ok.ActorKind != audit.KindUser ||
		ok.Actor != "jane@acme.test" || ok.ActorID != "u1" || ok.Target != "session" || ok.RequestID != "rid" {
		t.Fatalf("sign-in = %+v", ok)
	}
	if ok.Detail["method"] != SignInTOTP || ok.Detail["via"] != "admin" || ok.Detail["step"] != "second_factor" {
		t.Fatalf("sign-in detail = %v", ok.Detail)
	}
	if failed.Action != AuditSignInFailed || failed.Result != audit.ResultDenied || failed.Actor != "email-hash:J" || failed.Detail["reason"] != ReasonLocked {
		t.Fatalf("failed = %+v", failed)
	}
	if out.Action != AuditSignOut || out.Detail["all"] != true {
		t.Fatalf("sign-out = %+v", out)
	}
	assertAuditSafe(t, evs, "hunter2")
}

func TestSignInAuditFailedActorFallsBack(t *testing.T) {
	t.Parallel()

	a := &Admin{}
	if got := a.failedActor("jane@acme.test"); got != "unknown" {
		t.Fatalf("no hasher: %q", got)
	}
	a.deps.HashEmail = func(string) string { return "email-hash:x" }
	if got := a.failedActor(""); got != "unknown" {
		t.Fatalf("empty email: %q", got)
	}
}

func TestSignInAuditWithoutSinkIsNoop(t *testing.T) {
	t.Parallel()

	a := &Admin{}
	r := httptest.NewRequest("POST", "/admin/login", nil)
	a.auditSignIn(r, "", "x", SignInPassword, nil)
	a.auditSignInFailed(r, "", "x", ReasonCredentials, nil)
	a.auditSignOut(r, "", "x", false)
}
