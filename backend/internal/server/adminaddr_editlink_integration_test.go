//go:build integration

package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A real owner with a real session reaches the entity editor through /-/edit,
// at the default address and at a private one.
func TestEditLinkWithRealSession(t *testing.T) {
	e := newWireEnv(t, nil)
	e.addOwner(t, func() *Server { s, _ := e.server(t, "/admin"); return s }())
	id := uuid.NewString()
	for _, prefix := range []string{"/admin", swapStart} {
		t.Run(prefix, func(t *testing.T) {
			s, _ := e.server(t, prefix)
			owner, err := s.userRepo.GetByEmail(context.Background(), "owner@example.test")
			if err != nil {
				t.Fatal(err)
			}
			token, _, err := s.sessionStore.IssueAdmin(context.Background(), owner.ID, time.Hour, "", "")
			if err != nil {
				t.Fatal(err)
			}
			wantRedirect(t, editNav(s.Handler(), "?id="+id, token), prefix+"/entities/"+id+"/edit")
			if rec := editNav(s.Handler(), "?id="+id, ""); rec.Code != http.StatusNotFound {
				t.Errorf("signed out = %d, want 404", rec.Code)
			}
		})
	}
}
