package server

import (
	"context"
	"errors"
	"strings"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/backupcrypt"
)

// adminBackupKey implements admin.BackupKeyService over the saved backup
// encryption. It never returns the passphrase or the sealed bytes; the public
// key it shows is the age recipient, which is public by design.
type adminBackupKey struct {
	store   *backupcrypt.Store
	sealKey []byte
}

func (b *adminBackupKey) View(ctx context.Context) (admin.BackupKeyView, error) {
	set, _, err := b.store.Get(ctx)
	if err != nil {
		return admin.BackupKeyView{}, err
	}
	v := admin.BackupKeyView{Mode: admin.BackupKeyOff}
	switch set.Mode() {
	case "key":
		v.Mode, v.PublicKey = admin.BackupKeyPublic, set.Recipient
	case "passphrase":
		v.Mode = admin.BackupKeyPassphrase
	}
	if set.Mode() != "off" {
		if _, err := set.Key(b.sealKey); errors.Is(err, backupcrypt.ErrUnreadable) {
			v.Unreadable = true
		}
	}
	return v, nil
}

func (b *adminBackupKey) SavePublicKey(ctx context.Context, s string) error {
	if _, err := backupcrypt.ParseRecipient(s); err != nil {
		if errors.Is(err, backupcrypt.ErrPrivateKey) {
			return admin.ErrBackupPrivateKey
		}
		return admin.ErrBackupKeyInvalid
	}
	return b.store.SavePublicKey(ctx, strings.TrimSpace(s))
}

func (b *adminBackupKey) SavePassphrase(ctx context.Context, p string) error {
	if err := backupcrypt.ValidatePassphrase(p); err != nil {
		return admin.ErrBackupPassphraseShort
	}
	sealed, err := backupcrypt.SealPassphrase(b.sealKey, p)
	if err != nil {
		return err
	}
	return b.store.SavePassphrase(ctx, sealed)
}

func (b *adminBackupKey) Clear(ctx context.Context) error { return b.store.Clear(ctx) }
