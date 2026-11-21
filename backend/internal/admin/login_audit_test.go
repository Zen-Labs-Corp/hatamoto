package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/auth"
)

const (
	loginAuditPassword = "correct-horse-battery"
	loginAuditSecret   = "typed-wrong-password-9"
)

// loginAuditHasher is a stand-in for the keyed hash; it must never be the address.
func loginAuditHasher(email string) string {
	return auditlog.EmailHashPrefix + strings.Repeat("a", len(email))
}

func loginAuditForm(email, password string) url.Values {
	return url.Values{"email": {email}, "password": {password}}
}

func newLoginAuditAuthRig(t *testing.T) (*authRig, func() []AuditEvent) {
	t.Helper()
	g := newAuthRig(t, true)
	g.a.deps.HashEmail = loginAuditHasher
	return g, auditCapture(g.a)
}

func newLoginAuditTwoStepRig(t *testing.T) (*twoStepRig, func() []AuditEvent) {
	t.Helper()
	g := newTwoStepRig(t)
	g.a.deps.HashEmail = loginAuditHasher
	return g, auditCapture(g.a)
}

func loginAuditOne(t *testing.T, evs []AuditEvent, action string) AuditEvent {
	t.Helper()
	if len(evs) != 1 || evs[0].Action != action {
		t.Fatalf("events = %+v, want exactly one %s", evs, action)
	}
	return evs[0]
}

func TestLoginAuditPasswordOK(t *testing.T) {
	t.Parallel()

	g, read := newLoginAuditAuthRig(t)
	if rec := g.do("POST", "/admin/login", "", loginAuditForm("olga@example.com", loginAuditPassword), nil); rec.Code != 303 {
		t.Fatalf("login: %d", rec.Code)
	}
	evs := read()
	ev := loginAuditOne(t, evs, AuditSignIn)
	if ev.Result != audit.ResultOK || ev.ActorKind != audit.KindUser || ev.Actor != "olga@example.com" || ev.ActorID == "" ||
		ev.Detail["method"] != SignInPassword || ev.Target != "session" {
		t.Fatalf("row = %+v", ev)
	}
	assertAuditSafe(t, evs, loginAuditPassword)
}

func TestLoginAuditFailureShapeIsTheSameForKnownAndUnknownEmail(t *testing.T) {
	t.Parallel()

	var rows []AuditEvent
	for _, email := range []string{"olga@example.com", "nobody@example.com"} {
		g, read := newLoginAuditAuthRig(t)
		if rec := g.do("POST", "/admin/login", "", loginAuditForm(email, loginAuditSecret), nil); rec.Code != 422 {
			t.Fatalf("%s: %d", email, rec.Code)
		}
		evs := read()
		ev := loginAuditOne(t, evs, AuditSignInFailed)
		assertAuditSafe(t, evs, loginAuditSecret, email)
		if !strings.HasPrefix(ev.Actor, auditlog.EmailHashPrefix) || ev.ActorID != "" || ev.Result != audit.ResultDenied ||
			ev.ActorKind != audit.KindUser || ev.Detail["reason"] != ReasonCredentials {
			t.Fatalf("%s: row = %+v", email, ev)
		}
		rows = append(rows, ev)
	}
	a, b := rows[0], rows[1]
	if len(a.Detail) != len(b.Detail) || a.Action != b.Action || a.Result != b.Result || a.ActorKind != b.ActorKind || a.Target != b.Target {
		t.Fatalf("known and unknown differ in shape:\n%+v\n%+v", a, b)
	}
}

func TestLoginAuditLockedAccount(t *testing.T) {
	t.Parallel()

	g, read := newLoginAuditAuthRig(t)
	for range 3 {
		g.do("POST", "/admin/login", "", loginAuditForm("olga@example.com", loginAuditSecret), nil)
	}
	evs := read()
	if len(evs) != 3 {
		t.Fatalf("events = %d, want 3", len(evs))
	}
	g.do("POST", "/admin/login", "", loginAuditForm("olga@example.com", loginAuditPassword), nil)
	evs = read()
	last := evs[len(evs)-1]
	if len(evs) != 4 || last.Action != AuditSignInFailed || last.Detail["reason"] != ReasonLocked {
		t.Fatalf("locked attempt = %+v (n=%d)", last, len(evs))
	}
	assertAuditSafe(t, evs, loginAuditSecret, loginAuditPassword, "olga@example.com")
}

func TestLoginAuditSignOut(t *testing.T) {
	t.Parallel()

	for _, all := range []bool{false, true} {
		g, read := newLoginAuditAuthRig(t)
		form := url.Values{}
		if all {
			form.Set("all", "1")
		}
		if rec := g.do("POST", "/admin/logout", "owner-token", form, nil); rec.Code != 303 {
			t.Fatalf("logout: %d", rec.Code)
		}
		evs := read()
		ev := loginAuditOne(t, evs, AuditSignOut)
		if ev.Detail["all"] != all || ev.Actor == "" || ev.Result != audit.ResultOK {
			t.Fatalf("all=%v row = %+v", all, ev)
		}
		assertAuditSafe(t, evs)
	}
}

func TestLoginAuditTwoStepNoFactorSignsIn(t *testing.T) {
	t.Parallel()

	g, read := newLoginAuditTwoStepRig(t)
	g.tf.uid = [16]byte{1} // olga has no factor now
	if rec := g.do("POST", "/admin/login", "", loginAuditForm("olga@example.com", loginAuditPassword), nil); rec.Code != 303 {
		t.Fatalf("login: %d", rec.Code)
	}
	evs := read()
	ev := loginAuditOne(t, evs, AuditSignIn)
	if ev.Detail["method"] != SignInPassword || ev.Actor != "olga@example.com" {
		t.Fatalf("row = %+v", ev)
	}
	assertAuditSafe(t, evs, loginAuditPassword)
}

func TestLoginAuditTwoStepPasswordThenFactorPendingWritesNothingYet(t *testing.T) {
	t.Parallel()

	g, read := newLoginAuditTwoStepRig(t)
	g.password(t)
	if evs := read(); len(evs) != 0 {
		t.Fatalf("pending second step wrote %+v", evs)
	}
}

func TestLoginAuditTwoStepWrongPasswordHashesTheEmail(t *testing.T) {
	t.Parallel()

	g, read := newLoginAuditTwoStepRig(t)
	if rec := g.do("POST", "/admin/login", "", loginAuditForm("olga@example.com", loginAuditSecret), nil); rec.Code != 422 {
		t.Fatalf("login: %d", rec.Code)
	}
	evs := read()
	ev := loginAuditOne(t, evs, AuditSignInFailed)
	if !strings.HasPrefix(ev.Actor, auditlog.EmailHashPrefix) || ev.Detail["reason"] != ReasonCredentials {
		t.Fatalf("row = %+v", ev)
	}
	assertAuditSafe(t, evs, loginAuditSecret, "olga@example.com")
}

func TestLoginAuditTwoStepCodes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		code   string
		extra  url.Values
		action string
		method string
	}{
		{"wrong totp", "000000", nil, AuditSignInFailed, SignInTOTP},
		{"wrong recovery", "000000", url.Values{"recovery": {"1"}}, AuditSignInFailed, SignInRecovery},
		{"totp ok", "123456", nil, AuditSignIn, SignInTOTP},
		{"recovery ok", "ABCDEFGHJKMN", url.Values{"recovery": {"1"}}, AuditSignIn, SignInRecovery},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g, read := newLoginAuditTwoStepRig(t)
			g.second(g.password(t), tc.code, tc.extra)
			evs := read()
			ev := loginAuditOne(t, evs, tc.action)
			// A completed sign-in is attributed to the email, like the password
			// one; a failed second step keeps the user id.
			wantActor := ev.ActorID
			if tc.action == AuditSignIn {
				wantActor = "olga@example.com"
			}
			if ev.Detail["method"] != tc.method || ev.ActorID == "" || ev.Actor != wantActor {
				t.Fatalf("row = %+v", ev)
			}
			if tc.action == AuditSignInFailed && (ev.Result != audit.ResultDenied || ev.Detail["reason"] != ReasonSecondFactor) {
				t.Fatalf("failed row = %+v", ev)
			}
			assertAuditSafe(t, evs, tc.code)
		})
	}
}

func TestLoginAuditTwoStepRefusedSessionWritesOneRow(t *testing.T) {
	t.Parallel()

	g, read := newLoginAuditTwoStepRig(t)
	p := g.password(t)
	g.auth.verifiedErr = auth.ErrAccountLocked
	if rec := g.second(p, "123456", nil); rec.Code != 422 {
		t.Fatalf("refused: %d", rec.Code)
	}
	evs := read()
	ev := loginAuditOne(t, evs, AuditSignInFailed)
	if ev.Detail["reason"] != ReasonLocked || ev.Result != audit.ResultDenied {
		t.Fatalf("row = %+v", ev)
	}
	assertAuditSafe(t, evs, "123456")
}
