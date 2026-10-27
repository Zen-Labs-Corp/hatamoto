package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/session"
	"hatamoto.dev/core/internal/user"
)

// editSess accepts the token "good" as an owner's admin session.
type editSess struct{}

func (editSess) Validate(_ context.Context, tok string) (*session.Session, error) {
	if tok != "good" {
		return nil, auth.ErrInvalidToken
	}
	u, now := uuid.New(), time.Now()
	return &session.Session{ID: uuid.New(), Kind: session.KindAdmin, UserID: &u, LastUsedAt: &now}, nil
}
func (editSess) Revoke(context.Context, uuid.UUID) error           { return nil }
func (editSess) RevokeAllForUser(context.Context, uuid.UUID) error { return nil }

type editUsers struct{}

func (editUsers) GetByID(context.Context, uuid.UUID) (*user.User, error) {
	return &user.User{Role: user.RoleOwner}, nil
}

// editServer is swapServer with a stub session store, so a request can be
// signed in without a database. The stubs go through newAdmin because a swap
// rebuilds the admin with it.
func editServer(t *testing.T, resolved string) (http.Handler, *Server) {
	t.Helper()
	s, err := routeShell()
	if err != nil {
		t.Fatal(err)
	}
	s.addrStore = &fakeAddrStore{path: resolved}
	s.cfg.EnableCookieAuth = true
	s.cfg.Admin.Resolved = resolved
	s.validateSession = fakeSession
	s.hooks = hooks.New(s.logger)
	s.newAdmin = func(d admin.Deps) (*admin.Admin, error) {
		d.Sessions, d.Users = editSess{}, editUsers{}
		return admin.New(d)
	}
	s.setupRoutes()
	h := s.buildHandler()
	t.Cleanup(func() { s.stopProbes() })
	publicThemeFixture(t, s)
	s.registerNotFound()
	return h, s
}

// editNav is a user-activated navigation to /-/edit, signed in when cookie is set.
func editNav(h http.Handler, query, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/-/edit"+query, nil)
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func wantRedirect(t *testing.T, rec *httptest.ResponseRecorder, to string) {
	t.Helper()
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != to {
		t.Errorf("got %d Location %q, want 303 %q", rec.Code, rec.Header().Get("Location"), to)
	}
}

func TestEditLinkSignedIn(t *testing.T) {
	id := uuid.NewString()
	for _, prefix := range []string{"/admin", swapStart} {
		t.Run(prefix, func(t *testing.T) {
			h, _ := editServer(t, prefix)
			wantRedirect(t, editNav(h, "?id="+id, "good"), prefix+"/entities/"+id+"/edit")
			for name, rec := range map[string]*httptest.ResponseRecorder{
				"signed out":  editNav(h, "?id="+id, ""),
				"bad session": editNav(h, "?id="+id, "bad"),
				"bad id":      editNav(h, "?id=nope", "good"),
			} {
				if rec.Code != http.StatusNotFound {
					t.Errorf("%s = %d, want 404", name, rec.Code)
				}
			}

			req := httptest.NewRequest("GET", "/-/edit?id="+id, nil)
			req.Header.Set("Sec-Fetch-Mode", "navigate")
			req.Header.Set("Sec-Fetch-Dest", "document")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "good"})
			noUser := httptest.NewRecorder()
			h.ServeHTTP(noUser, req)
			if noUser.Code != http.StatusNotFound {
				t.Errorf("no Sec-Fetch-User = %d, want 404", noUser.Code)
			}
			miss := httptest.NewRecorder()
			h.ServeHTTP(miss, httptest.NewRequest("GET", "/definitely-not-a-page", nil))
			// Only the echoed request path (canonical, og:url) may differ.
			got := strings.ReplaceAll(noUser.Body.String(), "/-/edit", "/definitely-not-a-page")
			if got != miss.Body.String() {
				t.Errorf("refusal body differs from the not-found page")
			}

			login := httptest.NewRequest("GET", "/auth/login", nil)
			login.Header.Set("Sec-Fetch-Mode", "navigate")
			login.Header.Set("Sec-Fetch-Dest", "document")
			login.Header.Set("Sec-Fetch-User", "?1")
			login.Header.Set("Sec-Fetch-Site", "same-origin")
			login.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "good"})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, login)
			if rec.Code != http.StatusNotFound {
				t.Errorf("signed-in /auth/login = %d, want 404", rec.Code)
			}
		})
	}
}

// The link follows the live address: it needs no rebuild of the handler.
func TestEditLinkFollowsSwap(t *testing.T) {
	id := uuid.NewString()
	h, s := editServer(t, swapStart)
	wantRedirect(t, editNav(h, "?id="+id, "good"), swapStart+"/entities/"+id+"/edit")
	next := strongCandidate(t, s)
	if _, err := (&serverAddress{s: s}).Apply(context.Background(), admin.ApplyRequest{Path: next, By: "Ana"}); err != nil {
		t.Fatal(err)
	}
	wantRedirect(t, editNav(h, "?id="+id, "good"), next+"/entities/"+id+"/edit")
}
