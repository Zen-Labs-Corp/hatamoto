//go:build integration

package authbridge

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authstate"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/session"
	"hatamoto.dev/core/internal/user"
)

type bridgeRig struct {
	ctx      context.Context
	db       *database.DB
	users    *user.PostgresRepository
	sessions *session.Store
	keys     *auth.APIKeyService
	tokens   *auth.UserTokens
	idents   *user.IdentityStore
	prov     *Provisioner
}

func newBridgeRig(t *testing.T) *bridgeRig {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := database.New(context.Background(), config.DatabaseConfig{URL: dsn, MaxConnections: 8, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	r := &bridgeRig{
		ctx: context.Background(), db: db,
		users: user.NewPostgresRepository(db), sessions: session.NewStore(db),
		keys: auth.NewAPIKeyService(db, slog.Default()), tokens: auth.NewUserTokens(db),
		idents: user.NewIdentityStore(db),
	}
	r.prov = NewProvisioner(ProvisionerDeps{
		Users: r.users, Identities: r.idents, Sessions: r.sessions, Keys: r.keys, Tokens: r.tokens,
		AdminPath: func() string { return "/admin" }, Now: time.Now,
	})
	return r
}

func (r *bridgeRig) email() string {
	return "br-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + "@example.test"
}

// newUser creates a user with a password hash and removes it afterwards.
func (r *bridgeRig) newUser(t *testing.T, role user.Role) *user.User {
	t.Helper()
	ctx := database.WithSuperadmin(r.ctx)
	hash, err := auth.HashPassword("an-old-password-123")
	if err != nil {
		t.Fatal(err)
	}
	u, err := r.users.Create(ctx, &user.CreateUserInput{Email: r.email(), Role: role, Name: "B"}, hash)
	if err != nil {
		t.Fatal(err)
	}
	r.cleanupUser(t, u.ID)
	return u
}

func (r *bridgeRig) cleanupUser(t *testing.T, id uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		ctx := database.WithSuperadmin(r.ctx)
		_, _ = r.db.Pool().Exec(ctx, `DELETE FROM hatamoto_api_keys WHERE created_by = $1`, id)
		_, _ = r.db.Pool().Exec(ctx, `DELETE FROM hatamoto_users WHERE id = $1`, id)
	})
}

func (r *bridgeRig) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := r.db.Pool().QueryRow(database.WithSuperadmin(r.ctx), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// giveSessionsAndKeys leaves the user with 2 admin and 1 api session and 2 keys.
func (r *bridgeRig) giveSessionsAndKeys(t *testing.T, id uuid.UUID) {
	t.Helper()
	for i := 0; i < 2; i++ {
		if _, _, err := r.sessions.IssueAdmin(r.ctx, id, time.Hour, "ua", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := r.sessions.IssueAPI(r.ctx, id, time.Hour, "ua", ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := r.keys.Create(r.ctx, &id, &auth.CreateAPIKeyInput{Name: "k"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProvisionDisableRevokesAtOnce(t *testing.T) {
	r := newBridgeRig(t)
	u := r.newUser(t, user.RoleEditor)
	r.giveSessionsAndKeys(t, u.ID)

	if err := r.prov.Disable(r.ctx, u.ID.String()); err != nil {
		t.Fatal(err)
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_users WHERE id = $1 AND access_ends_at <= now()`, u.ID); n != 1 {
		t.Errorf("access end not set to now or earlier")
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_sessions WHERE user_id = $1 AND kind IN ('admin','api')`, u.ID); n != 0 {
		t.Errorf("%d sessions left", n)
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_api_keys WHERE created_by = $1 AND is_active`, u.ID); n != 0 {
		t.Errorf("%d active keys left", n)
	}
}

func TestProvisionDisableLastOwner(t *testing.T) {
	r := newBridgeRig(t)
	if n := r.count(t, `SELECT count(*) FROM hatamoto_users WHERE role = 'owner' AND access_ends_at IS NULL`); n != 0 {
		t.Skipf("%d other open owners in this database", n)
	}
	owner := r.newUser(t, user.RoleOwner)
	r.giveSessionsAndKeys(t, owner.ID)

	if err := r.prov.Disable(r.ctx, owner.ID.String()); !errors.Is(err, authstate.ErrLastOwner) {
		t.Fatalf("Disable last owner = %v, want ErrLastOwner", err)
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_users WHERE id = $1 AND access_ends_at IS NULL`, owner.ID); n != 1 {
		t.Error("access end was set")
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_sessions WHERE user_id = $1 AND kind IN ('admin','api')`, owner.ID); n != 3 {
		t.Errorf("%d sessions, want 3 untouched", n)
	}
}

func TestProvisionFindOrCreateIsPasswordless(t *testing.T) {
	r := newBridgeRig(t)
	email := r.email()
	in := authstate.NewUser{Email: "  " + strings.ToUpper(email) + " ", Name: "Ana", Role: "editor"}

	u1, created, err := r.prov.FindOrCreate(r.ctx, in)
	if err != nil || !created {
		t.Fatalf("first FindOrCreate = %v, %v", created, err)
	}
	id, _ := uuid.Parse(u1.ID)
	r.cleanupUser(t, id)
	u2, created, err := r.prov.FindOrCreate(r.ctx, in)
	if err != nil || created || u2.ID != u1.ID {
		t.Fatalf("second FindOrCreate = %+v, %v, %v", u2, created, err)
	}
	if u1.Email != email || u1.HasPassword {
		t.Errorf("user = %+v, want lower-case email and no password", u1)
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_users WHERE lower(email) = $1 AND password_hash IS NULL`, email); n != 1 {
		t.Errorf("%d rows for the email, want 1 with NULL hash", n)
	}
	if _, err := r.prov.Create(r.ctx, in); !errors.Is(err, authstate.ErrEmailTaken) {
		t.Errorf("Create of existing email = %v, want ErrEmailTaken", err)
	}
	if _, err := r.prov.Create(r.ctx, authstate.NewUser{Email: r.email(), Role: "root"}); !errors.Is(err, authstate.ErrInvalidRole) {
		t.Errorf("Create invalid role = %v, want ErrInvalidRole", err)
	}
}

func TestProvisionLinkIdentity(t *testing.T) {
	r := newBridgeRig(t)
	a, b := r.newUser(t, user.RoleEditor), r.newUser(t, user.RoleEditor)
	issuer, subject := "https://idp.example.test", uuid.NewString()

	if err := r.prov.LinkIdentity(r.ctx, a.ID.String(), issuer, subject); err != nil {
		t.Fatal(err)
	}
	if err := r.prov.LinkIdentity(r.ctx, b.ID.String(), issuer, subject); !errors.Is(err, authstate.ErrIdentityTaken) {
		t.Fatalf("second link = %v, want ErrIdentityTaken", err)
	}
	got, err := r.prov.FindByIdentity(r.ctx, issuer, subject)
	if err != nil || got.ID != a.ID.String() {
		t.Fatalf("FindByIdentity = %+v, %v", got, err)
	}
	if err := r.prov.UnlinkIdentity(r.ctx, a.ID.String(), issuer, subject); err != nil {
		t.Fatal(err)
	}
	if _, err := r.prov.FindByIdentity(r.ctx, issuer, subject); !errors.Is(err, authstate.ErrNotFound) {
		t.Fatalf("after unlink = %v, want ErrNotFound", err)
	}
	if _, err := r.prov.FindByID(r.ctx, "not-a-uuid"); !errors.Is(err, authstate.ErrNotFound) {
		t.Errorf("bad id = %v, want ErrNotFound", err)
	}
}

func TestProvisionPasswordSetLink(t *testing.T) {
	r := newBridgeRig(t)
	u, _, err := r.prov.FindOrCreate(r.ctx, authstate.NewUser{Email: r.email(), Name: "Ana", Role: "editor"})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := uuid.Parse(u.ID)
	r.cleanupUser(t, id)

	link, err := r.prov.IssuePasswordSetLink(r.ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "/admin/reset?token="
	if !strings.HasPrefix(link, prefix) {
		t.Fatalf("link = %q", link)
	}
	if _, err := r.tokens.ConsumeReset(r.ctx, strings.TrimPrefix(link, prefix), "new-password-123"); err != nil {
		t.Fatal(err)
	}
	svc := auth.NewService(r.users, nil, r.sessions)
	if _, err := svc.CheckPassword(r.ctx, u.Email, "new-password-123"); err != nil {
		t.Fatalf("password sign-in after set link: %v", err)
	}
}

func TestProvisionUpdateAndSetAccessEnd(t *testing.T) {
	r := newBridgeRig(t)
	u := r.newUser(t, user.RoleEditor)
	name := "Renamed"
	got, err := r.prov.Update(r.ctx, u.ID.String(), authstate.Changes{Name: &name})
	if err != nil || got.Name != name {
		t.Fatalf("Update = %+v, %v", got, err)
	}
	bad := "root"
	if _, err := r.prov.Update(r.ctx, u.ID.String(), authstate.Changes{Role: &bad}); !errors.Is(err, authstate.ErrInvalidRole) {
		t.Errorf("bad role = %v", err)
	}
	r.giveSessionsAndKeys(t, u.ID)
	future := time.Now().Add(time.Hour)
	if err := r.prov.SetAccessEnd(r.ctx, u.ID.String(), &future); err != nil {
		t.Fatal(err)
	}
	if n := r.count(t, `SELECT count(*) FROM hatamoto_sessions WHERE user_id = $1 AND kind IN ('admin','api')`, u.ID); n != 3 {
		t.Errorf("a future end revoked sessions: %d left", n)
	}
}
