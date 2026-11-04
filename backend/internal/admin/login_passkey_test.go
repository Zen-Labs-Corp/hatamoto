package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/twofactor"
)

// fakePasskeys models twofactor.Passkeys: a ceremony is single use and the
// assertion body decides the outcome: "ok", "nouv" (refused) or a user id.
type fakePasskeys struct {
	owner    uuid.UUID
	ceremony map[string]*uuid.UUID
	begun    []*uuid.UUID
}

func (f *fakePasskeys) BeginLogin(_ context.Context, uid *uuid.UUID) (json.RawMessage, string, error) {
	id := uuid.NewString()
	if f.ceremony == nil {
		f.ceremony = map[string]*uuid.UUID{}
	}
	f.ceremony[id] = uid
	f.begun = append(f.begun, uid)
	return json.RawMessage(`{"publicKey":{"challenge":"AAAA"}}`), id, nil
}

func (f *fakePasskeys) FinishLogin(_ context.Context, id string, uid *uuid.UUID, r *http.Request) (uuid.UUID, error) {
	want, ok := f.ceremony[id]
	delete(f.ceremony, id)
	if !ok || (uid == nil) != (want == nil) {
		return uuid.Nil, twofactor.ErrPasskeyFailed
	}
	var body struct{ ID string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.ID != "ok" {
		return uuid.Nil, twofactor.ErrPasskeyFailed
	}
	return f.owner, nil
}

func newPasskeyRig(t *testing.T) (*twoStepRig, *fakePasskeys) {
	t.Helper()
	fp := &fakePasskeys{}
	g := newTwoStepRig(t)
	fp.owner = g.tf.uid
	g.a.deps.Passkeys = fp
	return g, fp
}

func (g *twoStepRig) postJSONPending(path, pending, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if pending != "" {
		req.AddCookie(&http.Cookie{Name: "hm_2fa", Value: pending})
	}
	g.addFetchMeta(req, "", nil)
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	return rec
}

func (g *twoStepRig) passkeySignIn(pending, assertion string) *httptest.ResponseRecorder {
	rec := g.postJSONPending("/admin/login/passkey/begin", pending, "{}")
	var r passkeyBeginReply
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil || r.Ceremony == "" {
		g.t.Fatalf("begin: %d %s", rec.Code, rec.Body)
	}
	return g.postJSONPending("/admin/login/passkey/finish?c="+r.Ceremony, pending, assertion)
}

func TestPasskeyDiscoverableSignInIssuesSession(t *testing.T) {
	t.Parallel()

	g, fp := newPasskeyRig(t)
	rec := g.passkeySignIn("", `{"id":"ok"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"redirect"`) {
		t.Fatalf("finish: %d %s", rec.Code, rec.Body)
	}
	if c := cookieNamed(rec, "hatamoto_session"); c == nil || c.Value == "" {
		t.Fatal("no session cookie")
	}
	if len(fp.begun) != 1 || fp.begun[0] != nil {
		t.Error("without a pending cookie the ceremony is discoverable")
	}
}

func TestPasskeyRefusedAssertionIsGeneric401(t *testing.T) {
	t.Parallel()

	g, _ := newPasskeyRig(t)
	rec := g.passkeySignIn("", `{"id":"nouv"}`)
	if rec.Code != 401 || cookieNamed(rec, "hatamoto_session") != nil {
		t.Fatalf("refused assertion: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), sudoMessage) {
		t.Errorf("body: %s", rec.Body)
	}
}

func TestPasskeyAsSecondFactorNeedsThePendingCookie(t *testing.T) {
	t.Parallel()

	g, fp := newPasskeyRig(t)
	p := g.password(t)
	rec := g.passkeySignIn(p, `{"id":"ok"}`)
	if rec.Code != 200 {
		t.Fatalf("second factor: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `/admin/settings/users`) {
		t.Errorf("redirect does not follow the pending next: %s", rec.Body)
	}
	if c := cookieNamed(rec, "hm_2fa"); c == nil || c.MaxAge >= 0 {
		t.Errorf("pending cookie not cleared: %+v", c)
	}
	if fp.begun[0] == nil || *fp.begun[0] != g.tf.uid {
		t.Error("with a pending cookie the ceremony is bound to its user")
	}
	// A ceremony begun with the pending cookie cannot be finished without it.
	var r passkeyBeginReply
	_ = json.Unmarshal(g.postJSONPending("/admin/login/passkey/begin", p, "{}").Body.Bytes(), &r)
	if rec := g.postJSONPending("/admin/login/passkey/finish?c="+r.Ceremony, "", `{"id":"ok"}`); rec.Code != 401 {
		t.Fatalf("finish without pending: %d", rec.Code)
	}
}

func TestPasskeyEndpointsAbsentWhenOff(t *testing.T) {
	t.Parallel()

	g := newTwoStepRig(t)
	if rec := g.postJSONPending("/admin/login/passkey/begin", "", "{}"); rec.Code != 404 {
		t.Fatalf("begin: %d", rec.Code)
	}
	if body := g.do("GET", "/admin/login", "", nil, nil).Body.String(); strings.Contains(body, "data-passkey") {
		t.Error("passkey button shown with passkeys off")
	}
}

func TestPasskeyButtonsGolden(t *testing.T) {
	t.Parallel()

	g, _ := newPasskeyRig(t)
	body := g.do("GET", "/admin/login", "", nil, nil).Body.String()
	if !strings.Contains(body, "Sign in with a passkey") || !strings.Contains(body, "data-passkey") {
		t.Fatalf("login lacks the passkey button")
	}
	compareGolden(t, "pages/auth-login-passkey.html", normalizePage(body))
	p := g.password(t)
	g.tf.passkeys = 1
	two := g.withPending("GET", "/admin/login/2fa", p, nil).Body.String()
	if !strings.Contains(two, "Use a passkey instead") {
		t.Fatal("step 2 lacks the passkey button")
	}
	compareGolden(t, "pages/auth-two-step-passkey.html", normalizePage(two))
}
