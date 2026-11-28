package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/backupcrypt"
)

// memKeyDB is the one-row settings table in memory.
type memKeyDB struct {
	set   *backupcrypt.Settings
	execs int
}

func (m *memKeyDB) QueryRow(context.Context, string, ...any) pgx.Row { return fakeKeyRow{set: m.set} }

// Exec reads the argument shape of the store: an upsert has the recipient and
// the sealed passphrase, a delete has none.
func (m *memKeyDB) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	m.execs++
	if len(args) == 0 {
		m.set = nil
		return pgconn.CommandTag{}, nil
	}
	set := backupcrypt.Settings{Recipient: args[0].(string), UpdatedAt: time.Now()}
	if sealed, ok := args[1].([]byte); ok {
		set.PassphraseSealed = sealed
	}
	m.set = &set
	return pgconn.CommandTag{}, nil
}

func newTestAdminBackupKey(secret string, db *memKeyDB) *adminBackupKey {
	k := newBackupKeys(db, secret)
	return &adminBackupKey{store: k.store, sealKey: k.sealKey}
}

func TestAdminBackupKeyPublicKeyMapping(t *testing.T) {
	ctx := context.Background()
	db := &memKeyDB{}
	b := newTestAdminBackupKey("server-secret", db)

	private := "AGE-SECRET-" + "KEY-1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ"
	if err := b.SavePublicKey(ctx, private); !errors.Is(err, admin.ErrBackupPrivateKey) {
		t.Fatalf("private key = %v", err)
	}
	if err := b.SavePublicKey(ctx, "nonsense"); !errors.Is(err, admin.ErrBackupKeyInvalid) {
		t.Fatalf("invalid key = %v", err)
	}
	if db.execs != 0 {
		t.Fatalf("a refused key was stored (%d writes)", db.execs)
	}
	if err := b.SavePublicKey(ctx, " "+testRecipient+"\n"); err != nil {
		t.Fatal(err)
	}
	v, err := b.View(ctx)
	if err != nil || v.Mode != admin.BackupKeyPublic || v.PublicKey != testRecipient || v.Unreadable {
		t.Fatalf("view = %+v, %v", v, err)
	}
	if err := b.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _ := b.View(ctx); v.Mode != admin.BackupKeyOff || v.PublicKey != "" {
		t.Fatalf("view after Clear = %+v", v)
	}
}

func TestAdminBackupKeyPassphraseIsSealedAndNeverShown(t *testing.T) {
	ctx := context.Background()
	const pass = "correct horse battery staple"
	db := &memKeyDB{}
	b := newTestAdminBackupKey("server-secret", db)

	if err := b.SavePassphrase(ctx, "short"); !errors.Is(err, admin.ErrBackupPassphraseShort) || db.execs != 0 {
		t.Fatalf("short passphrase = %v, writes %d", err, db.execs)
	}
	if err := b.SavePassphrase(ctx, pass); err != nil {
		t.Fatal(err)
	}
	if string(db.set.PassphraseSealed) == pass || len(db.set.PassphraseSealed) == 0 {
		t.Fatal("the passphrase is not stored sealed")
	}
	v, err := b.View(ctx)
	if err != nil || v.Mode != admin.BackupKeyPassphrase || v.PublicKey != "" || v.Unreadable {
		t.Fatalf("view = %+v, %v", v, err)
	}
	// The same row read under another server secret cannot be opened.
	other := newTestAdminBackupKey("another-secret", db)
	if v, _ := other.View(ctx); v.Mode != admin.BackupKeyPassphrase || !v.Unreadable {
		t.Fatalf("view under another secret = %+v, want Unreadable", v)
	}
}
