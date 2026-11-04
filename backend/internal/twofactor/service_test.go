package twofactor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/user"
)

// fakeUsers is a user repository with the lock bookkeeping the service needs.
type fakeUsers struct {
	mu     sync.Mutex
	failed map[uuid.UUID]int
	locked map[uuid.UUID]time.Time
	max    int
}

func newFakeUsers() *fakeUsers {
	return &fakeUsers{failed: map[uuid.UUID]int{}, locked: map[uuid.UUID]time.Time{}}
}

func (f *fakeUsers) GetByID(_ context.Context, id uuid.UUID) (*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := &user.User{ID: id, Email: "demo@example.test"}
	if t, ok := f.locked[id]; ok {
		u.LockedUntil = &t
	}
	return u, nil
}

func (f *fakeUsers) RecordFailedLogin(_ context.Context, id uuid.UUID, max int, lock time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed[id]++
	if f.failed[id] >= max {
		f.locked[id] = time.Now().Add(lock)
	}
	return nil
}

func (f *fakeUsers) ResetFailedLogins(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.failed, id)
	delete(f.locked, id)
	return nil
}

type rig struct {
	svc   *Service
	repo  *MemoryStore
	users *fakeUsers
	uid   uuid.UUID
	now   time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{repo: NewMemoryStore(), users: newFakeUsers(), uid: uuid.New(), now: time.Unix(1_700_000_000, 0)}
	r.svc = NewService(r.repo, r.users, Config{
		SealKey: SealKeyFor("s"), RecoveryKey: RecoveryKey("s"), MaxFailed: 5, Lock: 15 * time.Minute,
		Now: func() time.Time { return r.now },
	})
	return r
}

// enrol runs the full enrolment and returns the secret and the recovery codes.
func (r *rig) enrol(t *testing.T) ([]byte, []string) {
	t.Helper()
	ctx := context.Background()
	b32s, uri, err := r.svc.BeginTOTP(ctx, r.uid, "ana@example.com")
	if err != nil || uri == "" {
		t.Fatalf("begin: %v", err)
	}
	secret, err := b32.DecodeString(b32s)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := r.svc.ConfirmTOTP(ctx, r.uid, Code(secret, StepAt(r.now)))
	if err != nil || len(codes) != 10 {
		t.Fatalf("confirm: %v (%d codes)", err, len(codes))
	}
	return secret, codes
}

func TestEnrolAndVerify(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	if has, _ := r.svc.Has(ctx, r.uid); has {
		t.Fatal("no factor yet")
	}
	secret, _ := r.enrol(t)
	if has, err := r.svc.Has(ctx, r.uid); err != nil || !has {
		t.Fatalf("Has = %v %v", has, err)
	}
	// The step used to confirm cannot be reused at sign-in.
	if err := r.svc.Verify(ctx, r.uid, Code(secret, StepAt(r.now))); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("replay of the confirming code = %v", err)
	}
	r.now = r.now.Add(30 * time.Second)
	good := Code(secret, StepAt(r.now))
	if err := r.svc.Verify(ctx, r.uid, good); err != nil {
		t.Fatalf("good code: %v", err)
	}
	if err := r.svc.Verify(ctx, r.uid, good); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("same code twice = %v", err)
	}
}

func TestSecretIsSealedAtRest(t *testing.T) {
	r := newRig(t)
	secret, _ := r.enrol(t)
	rec, _ := r.repo.GetTOTP(context.Background(), r.uid)
	if string(rec.SecretSealed) == string(secret) || len(rec.SecretSealed) <= len(secret) {
		t.Fatal("secret stored in the clear")
	}
}

func TestRecoveryCodeWorksOnce(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_, codes := r.enrol(t)
	if err := r.svc.Verify(ctx, r.uid, codes[3]); err != nil {
		t.Fatalf("recovery code: %v", err)
	}
	if err := r.svc.Verify(ctx, r.uid, codes[3]); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("spent code = %v", err)
	}
	if n, _ := r.repo.RecoveryLeft(ctx, r.uid); n != 9 {
		t.Fatalf("left = %d, want 9", n)
	}
	// Typed in another form it is the same code, and a stranger's code fails.
	if err := r.svc.Verify(ctx, r.uid, " "+NormalizeCode(codes[4])+" "); err != nil {
		t.Fatalf("normalised code: %v", err)
	}
	if err := r.svc.Verify(ctx, uuid.New(), codes[5]); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("code of another user = %v", err)
	}
}

func TestWrongCodesLockTheAccount(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	secret, _ := r.enrol(t)
	for i := 0; i < 5; i++ {
		if err := r.svc.Verify(ctx, r.uid, "000000"); !errors.Is(err, ErrWrongCode) {
			t.Fatalf("attempt %d = %v", i, err)
		}
	}
	r.now = r.now.Add(30 * time.Second)
	// Locked: even the right code is refused, with the same error.
	if err := r.svc.Verify(ctx, r.uid, Code(secret, StepAt(r.now))); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("locked account accepted a code: %v", err)
	}
}

func TestNeverLockedAccountIsNotLockedByWrongCodes(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.svc.cfg.NeverLock = func(email string) bool { return email == "demo@example.test" }
	r.enrol(t)
	for i := 0; i < 8; i++ {
		_ = r.svc.Verify(ctx, r.uid, "000000")
	}
	if r.users.failed[r.uid] != 0 {
		t.Fatalf("exempt account counted %d failures", r.users.failed[r.uid])
	}
}

func TestGoodCodeClearsFailures(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	secret, _ := r.enrol(t)
	for i := 0; i < 4; i++ {
		_ = r.svc.Verify(ctx, r.uid, "000000")
	}
	r.now = r.now.Add(30 * time.Second)
	if err := r.svc.Verify(ctx, r.uid, Code(secret, StepAt(r.now))); err != nil {
		t.Fatal(err)
	}
	if r.users.failed[r.uid] != 0 {
		t.Fatalf("failures not cleared: %d", r.users.failed[r.uid])
	}
}

func TestParallelReplayOnlyOneWins(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	secret, _ := r.enrol(t)
	r.now = r.now.Add(30 * time.Second)
	code := Code(secret, StepAt(r.now))
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r.svc.Verify(ctx, r.uid, code) == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d parallel uses of one code succeeded", wins)
	}
}

func TestEnrolmentGuards(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	if _, err := r.svc.ConfirmTOTP(ctx, r.uid, "123456"); !errors.Is(err, ErrNothingPending) {
		t.Fatalf("confirm without begin = %v", err)
	}
	if _, _, err := r.svc.BeginTOTP(ctx, r.uid, "a"); err != nil {
		t.Fatal(err)
	}
	if has, _ := r.svc.Has(ctx, r.uid); has {
		t.Fatal("an unconfirmed secret must not count as a factor")
	}
	if has, _ := r.svc.HasAnyFactor(ctx, r.uid); has {
		t.Fatal("an unconfirmed secret must not gate sign-in")
	}
	if _, err := r.svc.ConfirmTOTP(ctx, r.uid, "000000"); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("wrong confirm = %v", err)
	}
	r2 := newRig(t)
	r2.enrol(t)
	if _, _, err := r2.svc.BeginTOTP(ctx, r2.uid, "a"); !errors.Is(err, ErrAlreadyEnrolled) {
		t.Fatalf("begin over a confirmed secret = %v", err)
	}
}

func TestPasskeyCountsAsAnyFactorNotCodeFactor(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	if _, err := r.repo.AddPasskey(ctx, Passkey{UserID: r.uid, CredentialID: []byte("c1"), Name: "Laptop"}); err != nil {
		t.Fatal(err)
	}
	if has, _ := r.svc.HasAnyFactor(ctx, r.uid); !has {
		t.Fatal("a passkey is a factor at sign-in")
	}
	if has, _ := r.svc.Has(ctx, r.uid); has {
		t.Fatal("sudo cannot ask a passkey-only user for a typed code")
	}
}

func TestRegenerateAndRemove(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_, old := r.enrol(t)
	fresh, err := r.svc.RegenerateCodes(ctx, r.uid)
	if err != nil || len(fresh) != 10 {
		t.Fatal(err)
	}
	if err := r.svc.Verify(ctx, r.uid, old[0]); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("an old code survived regeneration: %v", err)
	}
	if err := r.svc.RemoveTOTP(ctx, r.uid); err != nil {
		t.Fatal(err)
	}
	if has, _ := r.svc.HasAnyFactor(ctx, r.uid); has {
		t.Fatal("factor remains")
	}
	if n, _ := r.repo.RecoveryLeft(ctx, r.uid); n != 0 {
		t.Fatalf("%d recovery codes outlived the last factor", n)
	}
}
