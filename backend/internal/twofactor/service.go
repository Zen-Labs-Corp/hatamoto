package twofactor

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/secretbox"
	"hatamoto.dev/core/internal/user"
)

// ErrWrongCode is returned for a wrong, replayed, spent or locked-out code. It
// never says which: callers show one neutral message.
var ErrWrongCode = errors.New("twofactor: wrong code")

// ErrAlreadyEnrolled is returned by BeginTOTP when an authenticator app is
// already confirmed for the user.
var ErrAlreadyEnrolled = errors.New("twofactor: authenticator app already set up")

// ErrNothingPending is returned by ConfirmTOTP when no enrolment was started.
var ErrNothingPending = errors.New("twofactor: no authenticator enrolment in progress")

// UserRepo is the part of the user repository the lock needs.
type UserRepo interface {
	GetByID(ctx context.Context, id uuid.UUID) (*user.User, error)
	RecordFailedLogin(ctx context.Context, id uuid.UUID, maxAttempts int, lockFor time.Duration) error
	ResetFailedLogins(ctx context.Context, id uuid.UUID) error
}

// Config configures a Service.
type Config struct {
	// SealKey seals TOTP secrets at rest (secretbox.DeriveKey(secret, "two-factor-totp")).
	SealKey []byte
	// RecoveryKey keys the recovery-code hashes (RecoveryKey(secret)).
	RecoveryKey []byte
	// MaxFailed and Lock are the account-lock policy shared with password sign-in.
	MaxFailed int
	Lock      time.Duration
	// Issuer names the site in the authenticator app.
	Issuer func(ctx context.Context) string
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// SealKeyFor derives the key that seals TOTP secrets from the server secret.
func SealKeyFor(serverSecret string) []byte {
	return secretbox.DeriveKey(serverSecret, "two-factor-totp")
}

// Service is the second-factor domain: authenticator app and recovery codes,
// with the per-user lock on wrong codes. It implements admin.SecondFactor
// structurally except for the error type (see Verify).
type Service struct {
	repo  Repository
	users UserRepo
	cfg   Config
	locks [64]sync.Mutex // serialise checks per user so parallel guesses cannot outrun the lock
}

// NewService builds a Service.
func NewService(repo Repository, users UserRepo, cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.MaxFailed <= 0 {
		cfg.MaxFailed = 5
	}
	if cfg.Lock <= 0 {
		cfg.Lock = 15 * time.Minute
	}
	return &Service{repo: repo, users: users, cfg: cfg}
}

// Repository exposes the storage, for the passkey wrapper built on the same one.
func (s *Service) Repository() Repository { return s.repo }

func (s *Service) lockFor(uid uuid.UUID) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write(uid[:])
	return &s.locks[h.Sum32()%uint32(len(s.locks))]
}

// HasCodeFactor reports a confirmed authenticator app (what sudo can ask for).
func (s *Service) HasCodeFactor(ctx context.Context, uid uuid.UUID) (bool, error) {
	r, err := s.repo.GetTOTP(ctx, uid)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return r.Confirmed, nil
}

// HasAnyFactor reports a confirmed authenticator app or at least one passkey
// (what makes password alone insufficient at sign-in).
func (s *Service) HasAnyFactor(ctx context.Context, uid uuid.UUID) (bool, error) {
	ok, err := s.HasCodeFactor(ctx, uid)
	if err != nil || ok {
		return ok, err
	}
	pk, err := s.repo.ListPasskeys(ctx, uid)
	return len(pk) > 0, err
}

// Has is HasCodeFactor under the sudo seam's name.
func (s *Service) Has(ctx context.Context, uid uuid.UUID) (bool, error) {
	return s.HasCodeFactor(ctx, uid)
}

// Locked reports whether the account is locked by failed sign-ins.
func (s *Service) Locked(ctx context.Context, uid uuid.UUID) (bool, error) {
	u, err := s.users.GetByID(database.WithSuperadmin(ctx), uid)
	if err != nil {
		return false, err
	}
	return u.LockedUntil != nil && u.LockedUntil.After(s.cfg.Now()), nil
}

// Fail counts a failed second-factor attempt against the account lock.
func (s *Service) Fail(ctx context.Context, uid uuid.UUID) {
	if err := s.users.RecordFailedLogin(database.WithSuperadmin(ctx), uid, s.cfg.MaxFailed, s.cfg.Lock); err != nil {
		fmt.Printf("failed to record failed second factor (non-fatal): %v\n", err)
	}
}

// Succeed clears the failure counter after a verified second factor.
func (s *Service) Succeed(ctx context.Context, uid uuid.UUID) {
	if err := s.users.ResetFailedLogins(database.WithSuperadmin(ctx), uid); err != nil {
		fmt.Printf("failed to reset failed logins (non-fatal): %v\n", err)
	}
}

// Verify checks a typed code: an authenticator-app code first, then a recovery
// code. A wrong code, a replayed step, a spent recovery code and a locked
// account all return ErrWrongCode; any other error means it could not decide.
// Wrong codes count against the account lock, a good one clears it.
func (s *Service) Verify(ctx context.Context, uid uuid.UUID, code string) error {
	mu := s.lockFor(uid)
	mu.Lock()
	defer mu.Unlock()

	locked, err := s.Locked(ctx, uid)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			return ErrWrongCode
		}
		return err
	}
	if locked {
		return ErrWrongCode
	}
	ok, err := s.verifyCode(ctx, uid, code)
	if err != nil {
		return err
	}
	if !ok {
		s.Fail(ctx, uid)
		return ErrWrongCode
	}
	s.Succeed(ctx, uid)
	return nil
}

func (s *Service) verifyCode(ctx context.Context, uid uuid.UUID, code string) (bool, error) {
	rec, err := s.repo.GetTOTP(ctx, uid)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return false, err
	}
	if err == nil && rec.Confirmed {
		secret, err := secretbox.Open(s.cfg.SealKey, uid[:], rec.SecretSealed)
		if err != nil {
			return false, fmt.Errorf("open authenticator secret: %w", err)
		}
		if step, ok := Check(secret, s.cfg.Now(), code, rec.LastStep); ok {
			// The conditional update makes a concurrent replay lose.
			adv, err := s.repo.AdvanceTOTPStep(ctx, uid, step)
			if err != nil {
				return false, err
			}
			return adv, nil
		}
	}
	return s.repo.SpendRecoveryCode(ctx, uid, HashRecoveryCode(s.cfg.RecoveryKey, code))
}

// BeginTOTP stores a fresh unconfirmed secret and returns it as text and as an
// otpauth URI. Calling it again before confirming replaces the secret.
func (s *Service) BeginTOTP(ctx context.Context, uid uuid.UUID, account string) (secretB32, uri string, err error) {
	secret, err := NewSecret()
	if err != nil {
		return "", "", err
	}
	sealed, err := secretbox.Seal(s.cfg.SealKey, uid[:], secret)
	if err != nil {
		return "", "", err
	}
	saved, err := s.repo.SaveTOTP(ctx, uid, sealed)
	if err != nil {
		return "", "", err
	}
	if !saved {
		return "", "", ErrAlreadyEnrolled
	}
	issuer := "Hatamoto"
	if s.cfg.Issuer != nil {
		if n := s.cfg.Issuer(ctx); n != "" {
			issuer = n
		}
	}
	return EncodeSecret(secret), ProvisioningURI(issuer, account, secret), nil
}

// PendingSecret returns the unconfirmed secret and its URI again, so the
// enrolment page can be shown twice (for example after a wrong code).
func (s *Service) PendingSecret(ctx context.Context, uid uuid.UUID, account string) (secretB32, uri string, err error) {
	rec, err := s.repo.GetTOTP(ctx, uid)
	if errors.Is(err, ErrNotFound) || (err == nil && rec.Confirmed) {
		return "", "", ErrNothingPending
	}
	if err != nil {
		return "", "", err
	}
	secret, err := secretbox.Open(s.cfg.SealKey, uid[:], rec.SecretSealed)
	if err != nil {
		return "", "", err
	}
	issuer := "Hatamoto"
	if s.cfg.Issuer != nil {
		if n := s.cfg.Issuer(ctx); n != "" {
			issuer = n
		}
	}
	return EncodeSecret(secret), ProvisioningURI(issuer, account, secret), nil
}

// ConfirmTOTP checks the first code of a pending enrolment, turns the
// authenticator on and returns 10 fresh recovery codes (plaintext, shown once).
func (s *Service) ConfirmTOTP(ctx context.Context, uid uuid.UUID, code string) ([]string, error) {
	rec, err := s.repo.GetTOTP(ctx, uid)
	if errors.Is(err, ErrNotFound) || (err == nil && rec.Confirmed) {
		return nil, ErrNothingPending
	}
	if err != nil {
		return nil, err
	}
	secret, err := secretbox.Open(s.cfg.SealKey, uid[:], rec.SecretSealed)
	if err != nil {
		return nil, fmt.Errorf("open authenticator secret: %w", err)
	}
	step, ok := Check(secret, s.cfg.Now(), code, 0)
	if !ok {
		return nil, ErrWrongCode
	}
	done, err := s.repo.ConfirmTOTP(ctx, uid, step)
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, ErrNothingPending
	}
	return s.RegenerateCodes(ctx, uid)
}

// RegenerateCodes replaces the user's recovery codes and returns the new ones.
func (s *Service) RegenerateCodes(ctx context.Context, uid uuid.UUID) ([]string, error) {
	codes, err := NewRecoveryCodes(RecoveryCodeCount)
	if err != nil {
		return nil, err
	}
	hashes := make([][]byte, len(codes))
	for i, c := range codes {
		hashes[i] = HashRecoveryCode(s.cfg.RecoveryKey, c)
	}
	if err := s.repo.ReplaceRecoveryCodes(ctx, uid, hashes); err != nil {
		return nil, err
	}
	return codes, nil
}

// RemoveTOTP turns the authenticator app off. The recovery codes go with it
// when no passkey remains, since they only matter while a factor exists.
func (s *Service) RemoveTOTP(ctx context.Context, uid uuid.UUID) error {
	if _, err := s.repo.DeleteTOTP(ctx, uid); err != nil {
		return err
	}
	return s.dropCodesIfNoFactor(ctx, uid)
}

func (s *Service) dropCodesIfNoFactor(ctx context.Context, uid uuid.UUID) error {
	has, err := s.HasAnyFactor(ctx, uid)
	if err != nil || has {
		return err
	}
	return s.repo.ReplaceRecoveryCodes(ctx, uid, nil)
}

// RemovePasskey deletes one passkey of the user.
func (s *Service) RemovePasskey(ctx context.Context, uid, id uuid.UUID) (bool, error) {
	ok, err := s.repo.DeletePasskey(ctx, uid, id)
	if err != nil || !ok {
		return ok, err
	}
	return true, s.dropCodesIfNoFactor(ctx, uid)
}

// Passkeys lists the user's passkeys, oldest first.
func (s *Service) Passkeys(ctx context.Context, uid uuid.UUID) ([]Passkey, error) {
	return s.repo.ListPasskeys(ctx, uid)
}

// Reset removes every factor of the user (the CLI lock-out path).
func (s *Service) Reset(ctx context.Context, uid uuid.UUID) (Removed, error) {
	return s.repo.Reset(ctx, uid)
}

// Status is what the Security page shows for one user.
func (s *Service) Status(ctx context.Context, uid uuid.UUID) (Factors, error) {
	m, err := s.repo.Factors(ctx, []uuid.UUID{uid})
	return m[uid], err
}

// Counts is Status for many users in one query (the Users table).
func (s *Service) Counts(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Factors, error) {
	return s.repo.Factors(ctx, ids)
}
