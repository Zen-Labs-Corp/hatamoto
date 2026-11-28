package server

import (
	"context"
	"fmt"

	"hatamoto.dev/core/internal/backupcrypt"
)

// backupKeySource gives the key backups are encrypted to.
type backupKeySource interface {
	// Current returns nil, nil when encryption is off. When it is on but the
	// key cannot be read it returns an error and the caller must not fall
	// back to a plain copy.
	Current(ctx context.Context) (*backupcrypt.Key, error)
}

// backupKeys reads the saved backup encryption key. Current fails closed.
type backupKeys struct {
	store   *backupcrypt.Store
	sealKey []byte
}

func newBackupKeys(db backupcrypt.Querier, serverSecret string) *backupKeys {
	return &backupKeys{store: backupcrypt.NewStore(db), sealKey: backupcrypt.SealKey(serverSecret)}
}

// Current implements backupKeySource. A database error also refuses, so a
// failed read never turns into a plain upload or download.
func (k *backupKeys) Current(ctx context.Context) (*backupcrypt.Key, error) {
	if k == nil {
		return nil, nil
	}
	set, found, err := k.store.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("load backup encryption: %w", err)
	}
	if !found {
		return nil, nil
	}
	return set.Key(k.sealKey)
}
