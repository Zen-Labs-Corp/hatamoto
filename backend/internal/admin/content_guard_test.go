package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/authztest"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/user"
)

func TestContentGuard(t *testing.T) {
	c, users := newRolesRig(t)
	ro, author := users[user.RoleReadOnly], users[user.RoleAuthor]

	if rec := c.do("POST", "/admin/entities", c.freshToken(ro.ID), url.Values{}, nil); rec.Code != 403 {
		t.Errorf("read-only POST /admin/entities = %d, want 403", rec.Code)
	}
	if rec := c.do("POST", "/admin/entities", c.freshToken(author.ID), url.Values{}, nil); rec.Code == 403 {
		t.Errorf("author POST /admin/entities blocked by the guard")
	}
	// Menus stay behind site.write.
	if rec := c.do("POST", "/admin/theme/menus", c.freshToken(author.ID), url.Values{}, nil); rec.Code != 403 {
		t.Errorf("author POST /admin/theme/menus = %d, want 403", rec.Code)
	}
	if rec := c.do("POST", "/admin/media/upload", c.freshToken(c.editor.ID), url.Values{}, nil); rec.Code == 403 {
		t.Errorf("editor POST /admin/media/upload blocked by the guard")
	}
	if rec := c.do("GET", "/admin/entities", c.freshToken(ro.ID), nil, nil); rec.Code != 200 {
		t.Errorf("read-only GET /admin/entities = %d, want 200", rec.Code)
	}
}

func TestContentGuardAsksContentWrite(t *testing.T) {
	rec := &authztest.Recorder{}
	authztest.Use(t, rec)
	c, users := newRolesRig(t)
	c.do("POST", "/admin/entities", c.freshToken(users[user.RoleAuthor].ID), url.Values{}, nil)
	var asked bool
	for _, call := range rec.Calls() {
		asked = asked || call.Action == authz.ActionContentWrite
		if call.Action == authz.ActionSiteWrite {
			t.Error("the content group asked site.write")
		}
	}
	if !asked {
		t.Error("content.write was not asked")
	}
}

func TestContentGuardDenyAll(t *testing.T) {
	authztest.Use(t, authztest.DenyAll)
	c, _ := newRolesRig(t)
	tok := c.freshToken(c.owner.ID)
	if rec := c.do("POST", "/admin/entities", tok, url.Values{}, nil); rec.Code != 403 {
		t.Errorf("owner POST /admin/entities under DenyAll = %d, want 403", rec.Code)
	}
	if rec := c.do("GET", "/admin/entities", tok, nil, nil); rec.Code == 403 {
		t.Errorf("owner GET /admin/entities blocked by the content guard")
	}
}

func TestRequireSessionAttachesSubject(t *testing.T) {
	c, users := newRolesRig(t)
	author := users[user.RoleAuthor]
	var got authz.Subject
	var ok bool
	h := c.a.RequireSession(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = authzctx.From(r.Context())
	}))
	req := httptest.NewRequest("GET", "/admin/x", nil)
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: c.freshToken(author.ID)})
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !ok || got.UserID != author.ID.String() || got.Role != "author" || got.KeyID != "" {
		t.Errorf("subject = %+v (present %v), want the author's session subject", got, ok)
	}
}

func TestRefuseNotAllowed(t *testing.T) {
	c, _ := newRolesRig(t)
	refused := &entity.NotAllowedError{Action: authz.ActionEntityPublish}

	rec := httptest.NewRecorder()
	if !c.a.refuseNotAllowed(rec, httptest.NewRequest("POST", "/admin/entities/x/status", nil), refused) {
		t.Fatal("a NotAllowedError was not refused")
	}
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "You cannot publish this entry") {
		t.Errorf("page refusal = %d, body has the sentence: %v", rec.Code, strings.Contains(rec.Body.String(), "You cannot publish this entry"))
	}

	if c.a.refuseNotAllowed(httptest.NewRecorder(), httptest.NewRequest("POST", "/x", nil), errors.New("boom")) {
		t.Error("an unrelated error was refused")
	}
}
