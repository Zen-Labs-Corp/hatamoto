package server

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/user"
)

type nameRepo struct {
	user.Repository
	u *user.User
}

func (r nameRepo) GetByID(context.Context, uuid.UUID) (*user.User, error) { return r.u, nil }

func TestMCPUserNameNeverEmail(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		name   string
		u      *user.User
		want   string
		wantOK bool
	}{
		{"named", &user.User{Email: "a@x.test", Name: str(" Jane ")}, "Jane", true},
		{"no name", &user.User{Email: "a@x.test"}, "", false},
		{"empty name", &user.User{Email: "a@x.test", Name: str("")}, "", false},
		{"name is the email", &user.User{Email: "a@x.test", Name: str("A@X.test")}, "", false},
		{"unknown user", nil, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Server{userRepo: nameRepo{u: c.u}}
			got, ok := s.mcpUserName(context.Background(), uuid.New())
			if got != c.want || ok != c.wantOK {
				t.Errorf("got (%q, %v), want (%q, %v)", got, ok, c.want, c.wantOK)
			}
		})
	}
}
