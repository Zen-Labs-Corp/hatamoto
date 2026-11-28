package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/backupcrypt"
)

// fakeKeyDB stands in for the pool: one optional settings row.
type fakeKeyDB struct {
	set   *backupcrypt.Settings
	err   error
	saved []any
}

type fakeKeyRow struct {
	set *backupcrypt.Settings
	err error
}

func (r fakeKeyRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.set == nil {
		return pgx.ErrNoRows
	}
	*dest[0].(*string) = r.set.Recipient
	*dest[1].(*[]byte) = r.set.PassphraseSealed
	*dest[2].(*time.Time) = r.set.UpdatedAt
	return nil
}

func (f *fakeKeyDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return fakeKeyRow{set: f.set, err: f.err}
}

func (f *fakeKeyDB) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	f.saved = append(f.saved, args...)
	return pgconn.CommandTag{}, nil
}

const testRecipient = "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"

func TestBackupKeysCurrent(t *testing.T) {
	ctx := context.Background()
	sealed, err := backupcrypt.SealPassphrase(backupcrypt.SealKey("other-secret"), "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name    string
		db      *fakeKeyDB
		wantKey bool
		wantErr error
		anyErr  bool
	}{
		{name: "off: no row", db: &fakeKeyDB{}},
		{name: "public key", db: &fakeKeyDB{set: &backupcrypt.Settings{Recipient: testRecipient}}, wantKey: true},
		{name: "passphrase sealed with another secret", db: &fakeKeyDB{set: &backupcrypt.Settings{PassphraseSealed: sealed}}, wantErr: backupcrypt.ErrUnreadable},
		{name: "database error refuses", db: &fakeKeyDB{err: errors.New("down")}, anyErr: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			k := newBackupKeys(c.db, "server-secret")
			got, err := k.Current(ctx)
			switch {
			case c.wantErr != nil && !errors.Is(err, c.wantErr):
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			case c.anyErr && err == nil:
				t.Fatal("a database error was not refused")
			case c.wantErr == nil && !c.anyErr && err != nil:
				t.Fatal(err)
			}
			if (got != nil) != c.wantKey {
				t.Fatalf("key = %v, want key %v", got, c.wantKey)
			}
		})
	}
	var none *backupKeys
	if k, err := none.Current(ctx); k != nil || err != nil {
		t.Fatalf("nil provider = %v, %v", k, err)
	}
}
