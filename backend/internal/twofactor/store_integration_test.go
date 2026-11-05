//go:build integration

package twofactor

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/migrations"
)

// scratchStore migrates a throwaway database and returns a Store plus a user id.
//
//	DATABASE_URL=... go test -tags integration ./internal/twofactor
func scratchStore(t *testing.T) (*Store, uuid.UUID, *pgxpool.Pool) {
	t.Helper()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_twofactor_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	prov, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prov.Up(ctx); err != nil {
		t.Fatal(err)
	}
	uid := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_users (id, email, role) VALUES ($1, $2, 'owner')`, uid, uid.String()+"@example.com"); err != nil {
		t.Fatal(err)
	}
	return NewStore(pool), uid, pool
}

func TestStoreTOTPAndReplayGuard(t *testing.T) {
	ctx := context.Background()
	s, uid, _ := scratchStore(t)
	if _, err := s.GetTOTP(ctx, uid); err != ErrNotFound {
		t.Fatalf("empty = %v", err)
	}
	if ok, err := s.SaveTOTP(ctx, uid, []byte("one")); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := s.SaveTOTP(ctx, uid, []byte("two")); !ok {
		t.Fatal("an unconfirmed secret must be replaceable")
	}
	if ok, _ := s.AdvanceTOTPStep(ctx, uid, 5); ok {
		t.Fatal("an unconfirmed secret must not advance")
	}
	if ok, _ := s.ConfirmTOTP(ctx, uid, 10); !ok {
		t.Fatal("confirm")
	}
	if ok, _ := s.ConfirmTOTP(ctx, uid, 11); ok {
		t.Fatal("double confirm")
	}
	if ok, _ := s.SaveTOTP(ctx, uid, []byte("three")); ok {
		t.Fatal("a confirmed secret must not be overwritten")
	}
	rec, _ := s.GetTOTP(ctx, uid)
	if !rec.Confirmed || rec.LastStep != 10 || string(rec.SecretSealed) != "two" {
		t.Fatalf("rec = %+v", rec)
	}

	// Many writers, one step: exactly one wins.
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := s.AdvanceTOTPStep(ctx, uid, 20); ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d concurrent advances won", wins)
	}
	if ok, _ := s.AdvanceTOTPStep(ctx, uid, 19); ok {
		t.Fatal("went backwards")
	}
}

func TestStoreRecoveryCodesSpendOnce(t *testing.T) {
	ctx := context.Background()
	s, uid, _ := scratchStore(t)
	h := [][]byte{[]byte("h1"), []byte("h2"), []byte("h3")}
	if err := s.ReplaceRecoveryCodes(ctx, uid, h); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := s.SpendRecoveryCode(ctx, uid, []byte("h2")); ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("a recovery code was spent %d times", wins)
	}
	if n, _ := s.RecoveryLeft(ctx, uid); n != 2 {
		t.Fatalf("left = %d", n)
	}
	if ok, _ := s.SpendRecoveryCode(ctx, uuid.New(), []byte("h1")); ok {
		t.Fatal("another user's code")
	}
	if err := s.ReplaceRecoveryCodes(ctx, uid, [][]byte{[]byte("n1")}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.SpendRecoveryCode(ctx, uid, []byte("h1")); ok {
		t.Fatal("a replaced code still works")
	}
}

func TestStorePasskeysCeremoniesAndReset(t *testing.T) {
	ctx := context.Background()
	s, uid, _ := scratchStore(t)
	id, err := s.AddPasskey(ctx, Passkey{UserID: uid, CredentialID: []byte("cred"), Credential: jsontext.Value(`{"a":1}`), Name: "Laptop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPasskey(ctx, Passkey{UserID: uid, CredentialID: []byte("cred"), Credential: jsontext.Value(`{}`), Name: "Dup"}); err == nil {
		t.Fatal("duplicate credential id accepted")
	}
	p, err := s.PasskeyByCredentialID(ctx, []byte("cred"))
	if err != nil || p.ID != id || p.Name != "Laptop" {
		t.Fatalf("lookup %+v %v", p, err)
	}
	if err := s.UpdatePasskey(ctx, id, jsontext.Value(`{"a":2}`), time.Now()); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListPasskeys(ctx, uid)
	if len(list) != 1 || list[0].LastUsedAt == nil || !bytes.Contains(list[0].Credential, []byte("2")) {
		t.Fatalf("list %+v", list)
	}

	cid := uuid.New()
	c := Ceremony{ID: cid, UserID: &uid, Purpose: "login", Data: jsontext.Value(`{}`), Expires: time.Now().Add(time.Minute)}
	if err := s.PutCeremony(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TakeCeremony(ctx, cid, "register", time.Now()); err != ErrNotFound {
		t.Fatalf("wrong purpose = %v", err)
	}
	if _, err := s.TakeCeremony(ctx, cid, "login", time.Now()); err != nil {
		t.Fatalf("first take = %v", err)
	}
	if _, err := s.TakeCeremony(ctx, cid, "login", time.Now()); err != ErrNotFound {
		t.Fatalf("second take = %v", err)
	}
	old := Ceremony{ID: uuid.New(), Purpose: "login", Data: jsontext.Value(`{}`), Expires: time.Now().Add(-time.Minute)}
	_ = s.PutCeremony(ctx, old)
	if _, err := s.TakeCeremony(ctx, old.ID, "login", time.Now()); err != ErrNotFound {
		t.Fatalf("expired = %v", err)
	}

	_, _ = s.SaveTOTP(ctx, uid, []byte("x"))
	_ = s.ReplaceRecoveryCodes(ctx, uid, [][]byte{[]byte("a"), []byte("b")})
	f, err := s.Factors(ctx, []uuid.UUID{uid})
	if err != nil || f[uid].Passkeys != 1 || f[uid].RecoveryLeft != 2 || f[uid].TOTP {
		t.Fatalf("factors %+v %v", f, err)
	}
	_, _ = s.ConfirmTOTP(ctx, uid, 1)
	if f, _ = s.Factors(ctx, []uuid.UUID{uid}); !f[uid].TOTP {
		t.Fatal("confirmed TOTP not counted")
	}
	r, err := s.Reset(ctx, uid)
	if err != nil || !r.TOTP || r.Passkeys != 1 || r.RecoveryCodes != 2 {
		t.Fatalf("reset %+v %v", r, err)
	}
	if f, _ = s.Factors(ctx, []uuid.UUID{uid}); f[uid] != (Factors{}) {
		t.Fatalf("after reset %+v", f[uid])
	}
	if r, _ = s.Reset(ctx, uid); r != (Removed{}) {
		t.Fatalf("second reset removed %+v", r)
	}
}
