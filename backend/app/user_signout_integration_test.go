//go:build integration

package app

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/previewlink"
	"hatamoto.dev/core/internal/session"
	"hatamoto.dev/core/internal/twofactor"
	"hatamoto.dev/core/internal/user"
)

type signOutFixture struct {
	ctx         context.Context
	db          *database.DB
	sessToken   string
	apiToken    string
	apiKey      string
	previewTok  string
	resetToken  string
	inviteToken string
}

// seedSignOut builds two users, two sessions, three API keys (one already
// revoked), an invite, a reset link, two live preview links and one revoked
// one, plus second-factor rows for the first user.
func seedSignOut(t *testing.T) *signOutFixture {
	t.Helper()
	dsn := scratchDSN(t)
	ctx := database.WithSuperadmin(context.Background())
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrateUp(ctx, db.Pool(), quietLogger()); err != nil {
		t.Fatal(err)
	}
	repo := user.NewPostgresRepository(db)
	ana, err := repo.Create(ctx, &user.CreateUserInput{Email: "ana@example.test", Role: user.RoleOwner}, "hash-a")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := repo.Create(ctx, &user.CreateUserInput{Email: "bob@example.test", Role: user.RoleEditor}, "hash-b")
	if err != nil {
		t.Fatal(err)
	}

	f := &signOutFixture{ctx: ctx, db: db}
	ss := session.NewStore(db)
	if f.sessToken, _, err = ss.IssueAdmin(ctx, ana.ID, time.Hour, "ua", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if f.apiToken, _, err = ss.IssueAPI(ctx, bob.ID, time.Hour, "ua", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}

	keys := auth.NewAPIKeyService(db, quietLogger())
	for i := 0; i < 3; i++ {
		k, err := keys.Create(ctx, &ana.ID, &auth.CreateAPIKeyInput{Name: "k" + string(rune('a'+i))})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			f.apiKey = k.PlainKey
		}
		if i == 2 {
			if err := keys.Revoke(ctx, k.ID, &ana.ID); err != nil {
				t.Fatal(err)
			}
		}
	}

	tokens := auth.NewUserTokens(db)
	if f.resetToken, err = tokens.IssueReset(ctx, bob.ID); err != nil {
		t.Fatal(err)
	}
	if f.inviteToken, _, err = tokens.IssueInvite(ctx, "new@example.test", user.RoleEditor, ana.ID, nil); err != nil {
		t.Fatal(err)
	}

	schemaID, entID := uuid.New(), uuid.New()
	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema) VALUES ($1,'so','so','{"type":"object"}')`, schemaID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,attributes) VALUES ($1,$2,'x','x','en','draft','{}')`, entID, schemaID); err != nil {
		t.Fatal(err)
	}
	pl := previewlink.New(db)
	for i := 0; i < 3; i++ {
		tok, l, err := pl.Create(ctx, entID, &ana.ID, "ana", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			f.previewTok = tok
		}
		if i == 2 {
			if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_preview_links SET revoked_at = now() WHERE id = $1`, l.ID); err != nil {
				t.Fatal(err)
			}
		}
	}

	st := twofactor.NewStore(db.Pool())
	if _, err := st.SaveTOTP(ctx, ana.ID, []byte("sealed")); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceRecoveryCodes(ctx, ana.ID, [][]byte{{1}, {2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO hatamoto_user_passkeys (user_id, credential_id, credential, name) VALUES ($1, '\x01', '{}', 'key')`, ana.ID); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *signOutFixture) count(t *testing.T, sql string) int {
	t.Helper()
	var n int
	if err := f.db.Pool().QueryRow(f.ctx, sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// snapshot hashes the rows that sign-out-all must never touch.
func (f *signOutFixture) snapshot(t *testing.T) string {
	t.Helper()
	var s string
	err := f.db.Pool().QueryRow(f.ctx, `SELECT
		COALESCE((SELECT md5(string_agg(u::text, ',' ORDER BY u.id)) FROM hatamoto_users u), '') ||
		COALESCE((SELECT md5(string_agg(p::text, ',' ORDER BY p.id)) FROM hatamoto_user_passkeys p), '') ||
		COALESCE((SELECT md5(string_agg(x::text, ',' ORDER BY x.user_id)) FROM hatamoto_user_totp x), '') ||
		COALESCE((SELECT md5(string_agg(r::text, ',' ORDER BY r.id)) FROM hatamoto_user_recovery_codes r), '')`).Scan(&s)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// state summarises every row set the command may change.
func (f *signOutFixture) state(t *testing.T) [6]int {
	return [6]int{
		f.count(t, `SELECT count(*) FROM hatamoto_sessions`),
		f.count(t, `SELECT count(*) FROM hatamoto_api_keys WHERE revoked_at IS NULL`),
		f.count(t, `SELECT count(*) FROM hatamoto_user_tokens WHERE used_at IS NULL`),
		f.count(t, `SELECT count(*) FROM hatamoto_preview_links WHERE revoked_at IS NULL`),
		f.count(t, `SELECT count(*) FROM hatamoto_api_keys WHERE is_active`),
		f.count(t, `SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'user.sign_out_all'`),
	}
}

func TestSignOutAllRefusesWithoutYes_Integration(t *testing.T) {
	f := seedSignOut(t)
	before := f.state(t)
	// No DATABASE_URL: if the refusal reached openDB the error would differ.
	t.Setenv("DATABASE_URL", "")
	err := run(f.ctx, cmdUserSignOutAll, []string{"user", "sign-out-all"}, quietLogger())
	var ee exitError
	if !errors.As(err, &ee) || ee.code != 1 {
		t.Fatalf("err = %v, want exitError{1}", err)
	}
	if after := f.state(t); after != before {
		t.Errorf("rows changed: %v -> %v", before, after)
	}
	if before != [6]int{2, 2, 2, 2, 2, 0} {
		t.Errorf("unexpected seed state %v", before)
	}
}

func TestSignOutAllRevokesEverything_Integration(t *testing.T) {
	f := seedSignOut(t)
	snap := f.snapshot(t)

	// Positive controls: every credential works before the command, so the
	// refusals after it can only come from the sign-out.
	ss := session.NewStore(f.db)
	keySvc := auth.NewAPIKeyService(f.db, quietLogger())
	if _, err := ss.Validate(f.ctx, f.sessToken); err != nil {
		t.Fatalf("admin session invalid before: %v", err)
	}
	if _, err := ss.Validate(f.ctx, f.apiToken); err != nil {
		t.Fatalf("api session invalid before: %v", err)
	}
	if _, err := keySvc.ValidateKey(f.ctx, f.apiKey); err != nil {
		t.Fatalf("api key invalid before: %v", err)
	}
	if _, ok := previewlink.New(f.db).Resolve(f.ctx, f.previewTok); !ok {
		t.Fatal("preview link does not resolve before")
	}
	if f.resetToken == "" || f.inviteToken == "" {
		t.Fatal("seed did not capture invite/reset tokens")
	}
	tokens := auth.NewUserTokens(f.db)
	if !tokens.ResetValid(f.ctx, f.resetToken) {
		t.Fatal("reset token unusable before")
	}
	if _, err := tokens.PeekInvite(f.ctx, f.inviteToken); err != nil {
		t.Fatalf("invite token unusable before: %v", err)
	}

	var out bytes.Buffer
	if err := runUserSignOutAll(f.ctx, f.db, &out); err != nil {
		t.Fatal(err)
	}
	want := "sessions      2 deleted\n" +
		"api keys      2 revoked\n" +
		"invite/reset  2 revoked\n" +
		"preview links 2 revoked\n" +
		"users, passwords and second factors are unchanged\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}

	if got := f.state(t); got != [6]int{0, 0, 0, 0, 0, 1} {
		t.Errorf("state = %v", got)
	}
	if got := f.count(t, `SELECT count(*) FROM hatamoto_api_keys WHERE revoked_at IS NULL OR is_active`); got != 0 {
		t.Errorf("live api keys: %d", got)
	}
	if f.snapshot(t) != snap {
		t.Error("users or second factors changed")
	}

	// Old credentials are refused at once (no caches).
	if _, err := ss.Validate(f.ctx, f.sessToken); err == nil {
		t.Error("admin session still valid")
	}
	if _, err := ss.Validate(f.ctx, f.apiToken); err == nil {
		t.Error("api session still valid")
	}
	if _, err := keySvc.ValidateKey(f.ctx, f.apiKey); err == nil {
		t.Error("api key still valid")
	}
	if _, ok := previewlink.New(f.db).Resolve(f.ctx, f.previewTok); ok {
		t.Error("preview link still resolves")
	}
	if tokens.ResetValid(f.ctx, f.resetToken) {
		t.Error("reset token still usable")
	}
	if _, err := tokens.PeekInvite(f.ctx, f.inviteToken); err == nil {
		t.Error("invite token still usable")
	}

	var actor, detail string
	if err := f.db.Pool().QueryRow(f.ctx,
		`SELECT actor, detail::text FROM hatamoto_admin_audit WHERE action = 'user.sign_out_all'`).Scan(&actor, &detail); err != nil {
		t.Fatal(err)
	}
	if actor != "cli" {
		t.Errorf("actor = %q", actor)
	}
	if got := f.count(t, `SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'user.sign_out_all'
		AND detail = '{"sessions":2,"api_keys":2,"invite_reset":2,"preview_links":2}'::jsonb`); got != 1 {
		t.Errorf("audit detail = %s", detail)
	}

	// Idempotent: a second run reports zeros and adds one more audit row.
	out.Reset()
	if err := runUserSignOutAll(f.ctx, f.db, &out); err != nil {
		t.Fatal(err)
	}
	wantZero := "sessions      0 deleted\n" +
		"api keys      0 revoked\n" +
		"invite/reset  0 revoked\n" +
		"preview links 0 revoked\n" +
		"users, passwords and second factors are unchanged\n"
	if out.String() != wantZero {
		t.Errorf("second run output:\n%s", out.String())
	}
	if got := f.count(t, `SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'user.sign_out_all'`); got != 2 {
		t.Errorf("audit rows = %d, want 2", got)
	}
}

func TestSignOutAllRollsBack_Integration(t *testing.T) {
	f := seedSignOut(t)
	before := f.state(t)
	if _, err := signOutAll(f.ctx, f.db, func() error { return errors.New("boom") }); err == nil {
		t.Fatal("hook error not returned")
	}
	if after := f.state(t); after != before {
		t.Errorf("rows changed despite failure: %v -> %v", before, after)
	}
}
