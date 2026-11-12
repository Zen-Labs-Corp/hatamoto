package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/user"
)

// slowRepo blocks UpdatePasswordHash until release is closed.
type slowRepo struct {
	*updaterRepo
	release chan struct{}
	mu      sync.Mutex
}

func (r *slowRepo) UpdatePasswordHash(ctx context.Context, id uuid.UUID, oldHash, newHash string) (bool, error) {
	<-r.release
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.updaterRepo.UpdatePasswordHash(ctx, id, oldHash, newHash)
}

type rehashCall struct {
	id               uuid.UUID
	oldHash, newHash string
}

// updaterRepo is the mock repo plus the optional hash-update method.
type updaterRepo struct {
	*mockUserRepo
	calls []rehashCall
	err   error
}

func (r *updaterRepo) UpdatePasswordHash(_ context.Context, id uuid.UUID, oldHash, newHash string) (bool, error) {
	r.calls = append(r.calls, rehashCall{id, oldHash, newHash})
	return r.err == nil, r.err
}

func rehashRig(hash string) (*Service, *updaterRepo, *user.User) {
	u := newTestUser(user.RoleOwner, hash)
	repo := &updaterRepo{mockUserRepo: &mockUserRepo{getByEmailReturn: u}}
	return NewService(repo, nil, nil), repo, u
}

func TestAuthenticate_RehashesLegacyHash(t *testing.T) {
	legacy := encodeWith(t, "correct-password", 65536, 1, 4)
	svc, repo, u := rehashRig(legacy)
	if _, err := svc.CheckPassword(context.Background(), u.Email, "correct-password"); err != nil {
		t.Fatal(err)
	}
	svc.rehashes.Wait()
	if len(repo.calls) != 1 {
		t.Fatalf("%d updates, want 1", len(repo.calls))
	}
	c := repo.calls[0]
	if c.id != u.ID || c.oldHash != legacy {
		t.Errorf("CAS on %v/%q, want %v/%q", c.id, c.oldHash, u.ID, legacy)
	}
	if !strings.Contains(c.newHash, "$m=19456,t=2,p=1$") {
		t.Errorf("new hash = %q", c.newHash)
	}
	if ok, err := VerifyPassword("correct-password", c.newHash); err != nil || !ok {
		t.Errorf("new hash does not verify: %v %v", ok, err)
	}
}

func TestAuthenticate_NoRehash(t *testing.T) {
	cur, err := HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, hash, password string
	}{
		{"wrong password", encodeWith(t, "correct-password", 65536, 1, 4), "nope"},
		{"reset marker", "!reset-required", "correct-password"},
		{"current params", cur, "correct-password"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo, u := rehashRig(tt.hash)
			_, _ = svc.CheckPassword(context.Background(), u.Email, tt.password)
			svc.rehashes.Wait()
			if len(repo.calls) != 0 {
				t.Errorf("%d updates, want none", len(repo.calls))
			}
		})
	}
}

func TestAuthenticate_RehashErrorKeepsSignIn(t *testing.T) {
	svc, repo, u := rehashRig(encodeWith(t, "correct-password", 65536, 1, 4))
	repo.err = errors.New("db down")
	if _, err := svc.CheckPassword(context.Background(), u.Email, "correct-password"); err != nil {
		t.Fatalf("sign-in failed on rehash error: %v", err)
	}
	svc.rehashes.Wait()
	if len(repo.calls) != 1 {
		t.Errorf("%d updates, want 1 attempt", len(repo.calls))
	}
}

func TestAuthenticate_RepoWithoutUpdaterSignsIn(t *testing.T) {
	u := newTestUser(user.RoleOwner, encodeWith(t, "correct-password", 65536, 1, 4))
	svc := NewService(&mockUserRepo{getByEmailReturn: u}, nil, nil)
	if _, err := svc.CheckPassword(context.Background(), u.Email, "correct-password"); err != nil {
		t.Fatal(err)
	}
}

// The upgrade runs after the response path: a slow (or gate-starved) rehash
// must not hold the sign-in, and a second sign-in does not start another.
func TestAuthenticate_RehashDoesNotBlockSignIn(t *testing.T) {
	u := newTestUser(user.RoleOwner, encodeWith(t, "correct-password", 65536, 1, 4))
	repo := &slowRepo{updaterRepo: &updaterRepo{mockUserRepo: &mockUserRepo{getByEmailReturn: u}}, release: make(chan struct{})}
	svc := NewService(repo, nil, nil)
	for range 2 { // one after the other: the mock hands out one shared user
		signIn := make(chan error, 1)
		go func() {
			_, err := svc.CheckPassword(context.Background(), u.Email, "correct-password")
			signIn <- err
		}()
		select {
		case err := <-signIn:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("sign-in waited for the rehash")
		}
		u.PasswordHash = encodeWith(t, "correct-password", 65536, 1, 4)
	}
	close(repo.release)
	svc.rehashes.Wait()
	if n := len(repo.calls); n != 1 {
		t.Errorf("%d updates for two sign-ins, want 1", n)
	}
}
