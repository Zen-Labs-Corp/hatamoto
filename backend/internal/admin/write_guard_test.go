package admin

import (
	"net/url"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/authztest"
	"hatamoto.dev/core/internal/user"
)

func TestWriteGuard(t *testing.T) {
	ro := &user.User{ID: uuid.New(), Email: "ro@example.com", Role: user.RoleReadOnly}
	g := newRig(t, func(d *Deps) { d.Users.(fakeUsers)[ro.ID] = ro })
	roTok := g.freshToken(ro.ID)

	if rec := g.do("POST", "/admin/media/upload", roTok, url.Values{}, nil); rec.Code != 403 {
		t.Errorf("read-only POST media/upload = %d, want 403", rec.Code)
	}
	if rec := g.do("POST", "/admin/profile/density", roTok, url.Values{"density": {"compact"}}, nil); rec.Code != 303 {
		t.Errorf("read-only POST profile/density = %d, want 303", rec.Code)
	}
	if rec := g.do("POST", "/admin/logout", g.freshToken(ro.ID), url.Values{}, nil); rec.Code != 303 {
		t.Errorf("read-only POST logout = %d, want 303", rec.Code)
	}
	if rec := g.do("GET", "/admin", roTok, nil, nil); rec.Code != 200 {
		t.Errorf("read-only GET /admin = %d, want 200", rec.Code)
	}
	if rec := g.do("POST", "/admin/media/upload", g.freshToken(g.editor.ID), url.Values{}, nil); rec.Code == 403 {
		t.Errorf("editor POST media/upload blocked by the guard")
	}
}

func TestWriteGuardDenyAll(t *testing.T) {
	authztest.Use(t, authztest.DenyAll)
	g := newRig(t)
	tok := g.freshToken(g.owner.ID)
	if rec := g.do("POST", "/admin/media/upload", tok, url.Values{}, nil); rec.Code != 403 {
		t.Errorf("owner POST media/upload under DenyAll = %d, want 403", rec.Code)
	}
}
