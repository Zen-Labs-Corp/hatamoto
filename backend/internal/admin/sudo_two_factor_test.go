package admin

import (
	"context"
	"encoding/base32"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/twofactor"
	"hatamoto.dev/core/internal/user"
)

// realFactor adapts the real two-factor service the way the server does.
type realFactor struct{ svc *twofactor.Service }

func (f realFactor) Has(ctx context.Context, id uuid.UUID) (bool, error) { return f.svc.Has(ctx, id) }
func (f realFactor) Verify(ctx context.Context, id uuid.UUID, c string) error {
	err := f.svc.Verify(ctx, id, c)
	if errors.Is(err, twofactor.ErrWrongCode) {
		return ErrWrongCode
	}
	return err
}

type lockUsers struct {
	mu     sync.Mutex
	failed int
	locked bool
}

func (u *lockUsers) GetByID(_ context.Context, id uuid.UUID) (*user.User, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	x := &user.User{ID: id}
	if u.locked {
		t := time.Now().Add(time.Hour)
		x.LockedUntil = &t
	}
	return x, nil
}
func (u *lockUsers) RecordFailedLogin(_ context.Context, _ uuid.UUID, max int, _ time.Duration) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.failed++
	if u.failed >= max {
		u.locked = true
	}
	return nil
}
func (u *lockUsers) ResetFailedLogins(context.Context, uuid.UUID) error { return nil }

func TestSudoAsksTheRealServiceForACode(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	users := &lockUsers{}
	svc := twofactor.NewService(twofactor.NewMemoryStore(), users, twofactor.Config{
		SealKey: twofactor.SealKeyFor("s"), RecoveryKey: twofactor.RecoveryKey("s"), MaxFailed: 3, Lock: time.Hour,
	})
	uid := g.owner.ID
	b32s, _, err := svc.BeginTOTP(context.Background(), uid, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(b32s)
	good := twofactor.Code(secret, twofactor.StepAt(time.Now()))
	if _, err := svc.ConfirmTOTP(context.Background(), uid, good); err != nil {
		t.Fatal(err)
	}
	g.a.SetSecondFactor(realFactor{svc})

	pw := url.Values{"password": {"correct-horse-battery"}}
	if rec := g.do("POST", g.a.sudoPath(), "owner-token", pw, nil); rec.Code == http.StatusSeeOther || g.elevated("owner-token") {
		t.Fatalf("password alone elevated: %d", rec.Code)
	}
	for i := 0; i < 3; i++ {
		f := url.Values{"password": {"correct-horse-battery"}, "code": {"000000"}}
		if rec := g.do("POST", g.a.sudoPath(), "owner-token", f, nil); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("wrong code %d: %d", i, rec.Code)
		}
	}
	if !users.locked {
		t.Fatal("wrong codes did not lock the user")
	}
	// Locked: even the right code is refused.
	f := url.Values{"password": {"correct-horse-battery"}, "code": {good}}
	if rec := g.do("POST", g.a.sudoPath(), "owner-token", f, nil); rec.Code != http.StatusUnprocessableEntity || g.elevated("owner-token") {
		t.Fatalf("locked user elevated: %d", rec.Code)
	}
}
