package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/testutil"
	"hatamoto.dev/core/internal/user"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

// mockUserRepo implements user.Repository for unit tests.
type mockUserRepo struct {
	// Return values
	getByEmailReturn   *user.User
	getByEmailErr      error
	getByIDReturn      *user.User
	getByIDErr         error
	listByEmailReturn  []*user.User
	listByEmailErr     error
	updateLastLoginErr error

	// Capture calls
	updateLastLoginCalled bool
	updateLastLoginID     uuid.UUID
	recordFailedCalled    bool
	recordFailedID        uuid.UUID
	resetFailedCalled     bool
	resetFailedID         uuid.UUID
}

func (m *mockUserRepo) Create(_ context.Context, _ *user.CreateUserInput, _ string) (*user.User, error) {
	return nil, nil
}

func (m *mockUserRepo) GetByID(_ context.Context, _ uuid.UUID) (*user.User, error) {
	return m.getByIDReturn, m.getByIDErr
}

func (m *mockUserRepo) GetByEmail(_ context.Context, _ string) (*user.User, error) {
	return m.getByEmailReturn, m.getByEmailErr
}

func (m *mockUserRepo) ListByEmail(_ context.Context, _ string) ([]*user.User, error) {
	return m.listByEmailReturn, m.listByEmailErr
}

func (m *mockUserRepo) Update(_ context.Context, _ uuid.UUID, _ *user.UpdateUserInput) (*user.User, error) {
	return nil, nil
}

func (m *mockUserRepo) UpdateLastLogin(_ context.Context, id uuid.UUID) error {
	m.updateLastLoginCalled = true
	m.updateLastLoginID = id
	return m.updateLastLoginErr
}

func (m *mockUserRepo) RecordFailedLogin(_ context.Context, id uuid.UUID, _ int, _ time.Duration) error {
	m.recordFailedCalled = true
	m.recordFailedID = id
	return nil
}

func (m *mockUserRepo) ResetFailedLogins(_ context.Context, id uuid.UUID) error {
	m.resetFailedCalled = true
	m.resetFailedID = id
	return nil
}

func (m *mockUserRepo) Delete(_ context.Context, _ uuid.UUID) error {
	return nil
}

func (m *mockUserRepo) List(_ context.Context, _, _ int) ([]*user.User, error) {
	return nil, nil
}

func (m *mockUserRepo) Count(_ context.Context) (int, error) {
	return 0, nil
}

// mockTenantLookup implements TenantLookup for unit tests.
// testTenantID is the synthetic singleton UUID used by tests in this
// file. Post-Path-B there is no real tenant identifier; tests still
// need a stable key for the mock-lookup map.
var testTenantID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

type mockTenantLookup struct {
	tenants map[uuid.UUID]struct {
		name string
		slug string
	}
	err error
}

func (m *mockTenantLookup) GetTenantNameSlug(_ context.Context) (string, string, error) {
	if m.err != nil {
		return "", "", m.err
	}
	if t, ok := m.tenants[testTenantID]; ok {
		return t.name, t.slug, nil
	}
	return "", "", errors.New("tenant not found")
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// newTestUser builds a user.User for auth-service tests. The tenantID
// parameter was dropped after Path B: user.User no longer carries a
// TenantID field, and the canonical tenant is resolved downstream.
func newTestUser(role user.Role, passwordHash string) *user.User {
	name := "Test User"
	return &user.User{
		ID:           uuid.New(),
		Email:        "test@example.com",
		PasswordHash: passwordHash,
		Role:         role,
		Name:         &name,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
}

// ---------------------------------------------------------------------------
// Login tests
//
// A successful login issues a database-backed session; that path is covered by
// the integration tests. These cover the credential checks that come before it.
// ---------------------------------------------------------------------------

func TestService_Login_RejectsBadCredentials(t *testing.T) {
	t.Parallel()
	hash, err := HashPassword("correct-password")
	testutil.RequireNoError(t, err)

	tests := []struct {
		name string
		repo *mockUserRepo
		pw   string
		want error
	}{
		{"wrong password", &mockUserRepo{getByEmailReturn: newTestUser(user.RoleOwner, hash)}, "nope", ErrInvalidCredentials},
		{"unknown user", &mockUserRepo{getByEmailErr: user.ErrUserNotFound}, "correct-password", ErrInvalidCredentials},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(tt.repo, nil, nil)
			_, err := svc.Login(context.Background(), &LoginRequest{Email: "test@example.com", Password: tt.pw}, "ua", "1.2.3.4")
			testutil.RequireErrorIs(t, err, tt.want)
		})
	}
}

func TestService_Login_NeedsSessions(t *testing.T) {
	t.Parallel()
	hash, err := HashPassword("correct-password")
	testutil.RequireNoError(t, err)
	svc := NewService(&mockUserRepo{getByEmailReturn: newTestUser(user.RoleOwner, hash)}, nil, nil)
	_, err = svc.Login(context.Background(), &LoginRequest{Email: "test@example.com", Password: "correct-password"}, "ua", "1.2.3.4")
	testutil.NotNil(t, err)
	testutil.False(t, errors.Is(err, ErrInvalidCredentials), "a correct password must not read as bad credentials")
}

// ---------------------------------------------------------------------------
// ListTenantMemberships tests
// ---------------------------------------------------------------------------

// Post-Path-B: ListTenantMemberships always returns the canonical
// singleton tenant (or nil/empty when no user/lookup is wired). The
// multi-tenant matrix tests have been collapsed into single-tenant
// equivalents — the function still exists for API compatibility with
// frontend code that calls /api/v1/auth/tenants.
func TestService_ListTenantMemberships(t *testing.T) {
	t.Parallel()

	owner := newTestUser(user.RoleOwner, "")
	owner.Email = "owner@example.com"

	tests := []struct {
		name         string
		email        string
		setupRepo    func() *mockUserRepo
		tenantLookup TenantLookup
		wantCount    int
		wantNil      bool
		wantErr      bool
	}{
		{
			name:  "returns canonical tenant membership",
			email: "owner@example.com",
			setupRepo: func() *mockUserRepo {
				return &mockUserRepo{listByEmailReturn: []*user.User{owner}}
			},
			tenantLookup: &mockTenantLookup{
				tenants: map[uuid.UUID]struct {
					name string
					slug string
				}{
					testTenantID: {name: "Acme Site", slug: "acme"},
				},
			},
			wantCount: 1,
		},
		{
			name:  "nil tenantLookup returns nil",
			email: "owner@example.com",
			setupRepo: func() *mockUserRepo {
				return &mockUserRepo{}
			},
			tenantLookup: nil,
			wantNil:      true,
		},
		{
			name:  "ListByEmail error propagates",
			email: "owner@example.com",
			setupRepo: func() *mockUserRepo {
				return &mockUserRepo{listByEmailErr: errors.New("db error")}
			},
			tenantLookup: &mockTenantLookup{
				tenants: map[uuid.UUID]struct {
					name string
					slug string
				}{},
			},
			wantErr: true,
		},
		{
			name:  "tenant lookup miss returns empty list",
			email: "owner@example.com",
			setupRepo: func() *mockUserRepo {
				return &mockUserRepo{listByEmailReturn: []*user.User{owner}}
			},
			tenantLookup: &mockTenantLookup{
				tenants: map[uuid.UUID]struct {
					name string
					slug string
				}{}, // canonical not registered → lookup fails → membership skipped
			},
			wantCount: 0,
		},
		{
			name:  "no users returns empty slice",
			email: "lonely@example.com",
			setupRepo: func() *mockUserRepo {
				return &mockUserRepo{listByEmailReturn: []*user.User{}}
			},
			tenantLookup: &mockTenantLookup{
				tenants: map[uuid.UUID]struct {
					name string
					slug string
				}{},
			},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := tt.setupRepo()
			svc := NewService(repo, tt.tenantLookup, nil)

			tenants, err := svc.ListTenantMemberships(context.Background(), tt.email)

			if tt.wantErr {
				testutil.Error(t, err)
				return
			}
			testutil.NoError(t, err)

			if tt.wantNil {
				testutil.Nil(t, tenants)
				return
			}

			testutil.Len(t, tenants, tt.wantCount)
		})
	}
}

func TestService_ListTenantMemberships_FieldValues(t *testing.T) {
	t.Parallel()

	testUser := newTestUser(user.RoleOwner, "")
	testUser.Email = "owner@example.com"

	lookup := &mockTenantLookup{
		tenants: map[uuid.UUID]struct {
			name string
			slug string
		}{
			testTenantID: {name: "My Org", slug: "my-org"},
		},
	}

	repo := &mockUserRepo{
		listByEmailReturn: []*user.User{testUser},
	}
	svc := NewService(repo, lookup, nil)

	tenants, err := svc.ListTenantMemberships(context.Background(), "owner@example.com")
	testutil.RequireNoError(t, err)
	testutil.RequireLen(t, tenants, 1)

	testutil.Equal(t, legacyAuthTenantID, tenants[0].ID)
	testutil.Equal(t, "My Org", tenants[0].Name)
	testutil.Equal(t, "my-org", tenants[0].Slug)
	testutil.Equal(t, "owner", tenants[0].Role)
}

// ---------------------------------------------------------------------------
// NewService constructor test
// ---------------------------------------------------------------------------

func TestNewService(t *testing.T) {
	t.Parallel()

	repo := &mockUserRepo{}
	lookup := &mockTenantLookup{}

	svc := NewService(repo, lookup, nil)

	testutil.NotNil(t, svc)
	testutil.Equal(t, repo, svc.userRepo)
	testutil.Equal(t, lookup, svc.tenantLookup)
}

// --- Login lockout (security-baseline) --------------------------------------

func TestService_Login_LockedAccountRejected(t *testing.T) {
	t.Parallel()
	hash, err := HashPassword("correct-password")
	testutil.RequireNoError(t, err)

	u := newTestUser(user.RoleOwner, hash)
	future := time.Now().Add(10 * time.Minute)
	u.LockedUntil = &future
	repo := &mockUserRepo{getByEmailReturn: u}
	svc := NewService(repo, nil, nil)

	// Even with the CORRECT password, a locked account is refused.
	_, err = svc.Login(context.Background(), &LoginRequest{Email: u.Email, Password: "correct-password"}, "ua", "1.2.3.4")
	testutil.RequireErrorIs(t, err, ErrAccountLocked)
	testutil.False(t, repo.resetFailedCalled, "must not reset while locked")
}

func TestService_Login_ExpiredLockAllowsLogin(t *testing.T) {
	t.Parallel()
	hash, err := HashPassword("correct-password")
	testutil.RequireNoError(t, err)

	u := newTestUser(user.RoleOwner, hash)
	past := time.Now().Add(-1 * time.Minute)
	u.LockedUntil = &past // lock already expired
	repo := &mockUserRepo{getByEmailReturn: u}
	svc := NewService(repo, nil, nil)

	// No session store: the login stops after the password check.
	_, err = svc.Login(context.Background(), &LoginRequest{Email: u.Email, Password: "correct-password"}, "ua", "1.2.3.4")
	testutil.False(t, errors.Is(err, ErrAccountLocked), "an expired lock must not refuse the login")
	testutil.True(t, repo.resetFailedCalled, "successful login resets failure state")
}

func TestService_Login_WrongPasswordRecordsFailure(t *testing.T) {
	t.Parallel()
	hash, err := HashPassword("correct-password")
	testutil.RequireNoError(t, err)

	u := newTestUser(user.RoleOwner, hash)
	repo := &mockUserRepo{getByEmailReturn: u}
	svc := NewService(repo, nil, nil)

	_, err = svc.Login(context.Background(), &LoginRequest{Email: u.Email, Password: "wrong"}, "ua", "1.2.3.4")
	testutil.RequireErrorIs(t, err, ErrInvalidCredentials)
	testutil.True(t, repo.recordFailedCalled, "a wrong password records a failed attempt")
	testutil.Equal(t, u.ID, repo.recordFailedID)
	testutil.False(t, repo.resetFailedCalled)
}

func TestService_Login_SuccessResetsFailures(t *testing.T) {
	t.Parallel()
	hash, err := HashPassword("correct-password")
	testutil.RequireNoError(t, err)

	u := newTestUser(user.RoleOwner, hash)
	u.FailedLoginAttempts = 3 // some prior failures, not yet locked
	repo := &mockUserRepo{getByEmailReturn: u}
	svc := NewService(repo, nil, nil)

	_, _ = svc.Login(context.Background(), &LoginRequest{Email: u.Email, Password: "correct-password"}, "ua", "1.2.3.4")
	testutil.True(t, repo.resetFailedCalled, "success clears the failure counter")
	testutil.False(t, repo.recordFailedCalled)
}

func TestService_Login_NeverLockAccountIsNotLocked(t *testing.T) {
	t.Parallel()
	hash, err := HashPassword("correct-password")
	testutil.RequireNoError(t, err)

	u := newTestUser(user.RoleOwner, hash)
	repo := &mockUserRepo{getByEmailReturn: u}
	svc := NewService(repo, nil, nil)
	svc.NeverLock(" TEST@example.com ")

	// Failures are not recorded, so five of them cannot lock the shared login.
	_, err = svc.Login(context.Background(), &LoginRequest{Email: u.Email, Password: "wrong"}, "ua", "1.2.3.4")
	testutil.RequireErrorIs(t, err, ErrInvalidCredentials)
	testutil.False(t, repo.recordFailedCalled, "a failure on the exempt account must not count")

	// A lock that is already set does not stop it either.
	future := time.Now().Add(10 * time.Minute)
	u.LockedUntil = &future
	_, err = svc.Login(context.Background(), &LoginRequest{Email: u.Email, Password: "correct-password"}, "ua", "1.2.3.4")
	testutil.False(t, errors.Is(err, ErrAccountLocked), "the exempt account is never refused as locked")

	// Any other account still locks.
	other := &mockUserRepo{getByEmailReturn: newTestUser(user.RoleOwner, hash)}
	svc2 := NewService(other, nil, nil)
	svc2.NeverLock("someone-else@example.com")
	_, _ = svc2.Login(context.Background(), &LoginRequest{Email: "test@example.com", Password: "wrong"}, "ua", "1.2.3.4")
	testutil.True(t, other.recordFailedCalled, "other accounts keep the lockout")
}
