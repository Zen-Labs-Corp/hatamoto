package pluginhost

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
)

type sessionRig struct {
	reg   *RouteRegistry
	calls []sdk.CommenterSession
	err   error
}

// newSessionRig serves one canned plugin response on a public (or admin) mount.
func newSessionRig(t *testing.T, public, granted bool, res sdk.HTTPResponse) *sessionRig {
	t.Helper()
	out, err := jsonx.MarshalPinned(res)
	if err != nil {
		t.Fatal(err)
	}
	call := func(context.Context, string, string, []byte) ([]byte, error) { return out, nil }
	s := &sessionRig{reg: NewRouteRegistry(call, public, slog.New(slog.NewTextHandler(io.Discard, nil)))}
	s.reg.identity = func(string) bool { return granted }
	s.reg.session = func(w http.ResponseWriter, _ *http.Request, _ string, cs sdk.CommenterSession) error {
		s.calls = append(s.calls, cs)
		if s.err == nil {
			http.SetCookie(w, &http.Cookie{Name: "hm_c", Value: "v"})
		}
		return s.err
	}
	s.reg.Set("capa", &sdk.Manifest{Name: "capa", Cookies: []string{"unlock"}, Routes: []sdk.Route{
		{Method: "GET", Path: "/in", Public: public},
		{Method: "POST", Path: "/in", Public: public},
	}})
	return s
}

func (s *sessionRig) serve(method string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/p/capa/in", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	s.reg.Serve(rec, req, "capa", "in", nil)
	return rec
}

func sessionResponse(cs *sdk.CommenterSession) sdk.HTTPResponse {
	return sdk.HTTPResponse{Status: 303, Headers: map[string]string{"Location": "/done", "Cache-Control": "public, max-age=60"},
		SetCookies: []sdk.SetCookie{{Name: "unlock", Value: "1"}}, CommenterSession: cs}
}

func TestCommenterSessionApplied(t *testing.T) {
	s := newSessionRig(t, true, true, sessionResponse(&sdk.CommenterSession{Account: "u1"}))
	rec := s.serve("POST")
	if rec.Code != 303 || len(s.calls) != 1 || s.calls[0].Account != "u1" {
		t.Fatalf("code %d, calls %+v", rec.Code, s.calls)
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Location") != "/done" {
		t.Errorf("headers: %v", rec.Header())
	}
	if len(rec.Result().Cookies()) != 2 {
		t.Errorf("cookies: %v", rec.Header()["Set-Cookie"])
	}
}

func TestCommenterSessionRefusedByGate(t *testing.T) {
	signOutAndAccount := &sdk.CommenterSession{SignOut: true, Account: "x"}
	cases := map[string]struct {
		public, granted bool
		method          string
		cs              *sdk.CommenterSession
	}{
		"GET":      {true, true, "GET", &sdk.CommenterSession{Account: "u1"}},
		"no grant": {true, false, "POST", &sdk.CommenterSession{Account: "u1"}},
		"bad":      {true, true, "POST", signOutAndAccount},
	}
	for name, c := range cases {
		s := newSessionRig(t, c.public, c.granted, sessionResponse(c.cs))
		rec := s.serve(c.method)
		if rec.Code != http.StatusBadGateway || len(s.calls) != 0 || len(rec.Header()["Set-Cookie"]) != 0 {
			t.Errorf("%s: code %d, calls %d, cookies %v", name, rec.Code, len(s.calls), rec.Header()["Set-Cookie"])
		}
	}
}

// The admin mount needs an authorised user to reach the response writer, so the
// writer is driven directly.
func TestCommenterSessionRefusedOnAdminMount(t *testing.T) {
	s := newSessionRig(t, false, true, sdk.HTTPResponse{})
	res := sdk.HTTPResponse{Status: 303, CommenterSession: &sdk.CommenterSession{Account: "u1"}}
	rec := httptest.NewRecorder()
	s.reg.writeRouteResponse(rec, httptest.NewRequest("POST", "/api/v1/p/capa/in", nil), res, sdk.Manifest{Name: "capa"}, false)
	if rec.Code != http.StatusBadGateway || len(s.calls) != 0 {
		t.Errorf("code %d, calls %d", rec.Code, len(s.calls))
	}
}

func TestCommenterSessionCallbackRefusal(t *testing.T) {
	for err, want := range map[error]int{ErrSessionCrossOrigin: 403, ErrSessionUnavailable: 503, io.ErrUnexpectedEOF: 502} {
		s := newSessionRig(t, true, true, sessionResponse(&sdk.CommenterSession{Account: "u1"}))
		s.err = err
		rec := s.serve("POST")
		if rec.Code != want || len(rec.Header()["Set-Cookie"]) != 0 || rec.Header().Get("Location") != "" {
			t.Errorf("%v: code %d, headers %v", err, rec.Code, rec.Header())
		}
	}
	s := newSessionRig(t, true, true, sessionResponse(&sdk.CommenterSession{Account: "u1"}))
	s.reg.session = nil
	if rec := s.serve("POST"); rec.Code != http.StatusServiceUnavailable || len(rec.Header()["Set-Cookie"]) != 0 {
		t.Errorf("no session func: %d %v", rec.Code, rec.Header())
	}
}

func TestRouteWithoutCommenterSessionUnchanged(t *testing.T) {
	s := newSessionRig(t, true, true, sessionResponse(nil))
	rec := s.serve("POST")
	if rec.Code != 303 || len(s.calls) != 0 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("code %d calls %d headers %v", rec.Code, len(s.calls), rec.Header())
	}
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].Name != "hmp_capa_unlock" {
		t.Errorf("cookies: %v", rec.Header()["Set-Cookie"])
	}
}
