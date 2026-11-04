package twofactor

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is an in-memory Repository for tests of this package and of its
// callers. It keeps the single-use and replay semantics of the Postgres store.
type MemoryStore struct {
	mu       sync.Mutex
	totp     map[uuid.UUID]*TOTPRecord
	codes    map[uuid.UUID][]memCode
	passkeys []Passkey
	ceremony map[uuid.UUID]Ceremony
}

type memCode struct {
	hash []byte
	used bool
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{totp: map[uuid.UUID]*TOTPRecord{}, codes: map[uuid.UUID][]memCode{}, ceremony: map[uuid.UUID]Ceremony{}}
}

var _ Repository = (*MemoryStore)(nil)

func (m *MemoryStore) GetTOTP(_ context.Context, uid uuid.UUID) (*TOTPRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.totp[uid]
	if !ok {
		return nil, ErrNotFound
	}
	c := *r
	return &c, nil
}

func (m *MemoryStore) SaveTOTP(_ context.Context, uid uuid.UUID, sealed []byte) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.totp[uid]; ok && r.Confirmed {
		return false, nil
	}
	m.totp[uid] = &TOTPRecord{SecretSealed: append([]byte(nil), sealed...)}
	return true, nil
}

func (m *MemoryStore) ConfirmTOTP(_ context.Context, uid uuid.UUID, step int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.totp[uid]
	if !ok || r.Confirmed {
		return false, nil
	}
	r.Confirmed, r.LastStep = true, step
	return true, nil
}

func (m *MemoryStore) AdvanceTOTPStep(_ context.Context, uid uuid.UUID, step int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.totp[uid]
	if !ok || !r.Confirmed || r.LastStep >= step {
		return false, nil
	}
	r.LastStep = step
	return true, nil
}

func (m *MemoryStore) DeleteTOTP(_ context.Context, uid uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.totp[uid]
	delete(m.totp, uid)
	return ok, nil
}

func (m *MemoryStore) ReplaceRecoveryCodes(_ context.Context, uid uuid.UUID, hashes [][]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cs := make([]memCode, 0, len(hashes))
	for _, h := range hashes {
		cs = append(cs, memCode{hash: append([]byte(nil), h...)})
	}
	m.codes[uid] = cs
	return nil
}

func (m *MemoryStore) SpendRecoveryCode(_ context.Context, uid uuid.UUID, hash []byte) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.codes[uid] {
		if c := &m.codes[uid][i]; !c.used && bytes.Equal(c.hash, hash) {
			c.used = true
			return true, nil
		}
	}
	return false, nil
}

func (m *MemoryStore) RecoveryLeft(_ context.Context, uid uuid.UUID) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recoveryLeft(uid), nil
}

func (m *MemoryStore) recoveryLeft(uid uuid.UUID) int {
	n := 0
	for _, c := range m.codes[uid] {
		if !c.used {
			n++
		}
	}
	return n
}

func (m *MemoryStore) ListPasskeys(_ context.Context, uid uuid.UUID) ([]Passkey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Passkey
	for _, p := range m.passkeys {
		if p.UserID == uid {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *MemoryStore) AddPasskey(_ context.Context, p Passkey) (uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, q := range m.passkeys {
		if bytes.Equal(q.CredentialID, p.CredentialID) {
			return uuid.Nil, ErrNotFound
		}
	}
	p.ID, p.CreatedAt = uuid.New(), time.Now()
	m.passkeys = append(m.passkeys, p)
	return p.ID, nil
}

func (m *MemoryStore) PasskeyByCredentialID(_ context.Context, credID []byte) (*Passkey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.passkeys {
		if bytes.Equal(p.CredentialID, credID) {
			c := p
			return &c, nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryStore) UpdatePasskey(_ context.Context, id uuid.UUID, credential json.RawMessage, usedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.passkeys {
		if m.passkeys[i].ID == id {
			m.passkeys[i].Credential = append(json.RawMessage(nil), credential...)
			m.passkeys[i].LastUsedAt = &usedAt
		}
	}
	return nil
}

func (m *MemoryStore) DeletePasskey(_ context.Context, uid, id uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, p := range m.passkeys {
		if p.ID == id && p.UserID == uid {
			m.passkeys = append(m.passkeys[:i], m.passkeys[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func (m *MemoryStore) PutCeremony(_ context.Context, c Ceremony) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ceremony[c.ID] = c
	return nil
}

func (m *MemoryStore) TakeCeremony(_ context.Context, id uuid.UUID, purpose string, now time.Time) (*Ceremony, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.ceremony[id]
	if !ok || c.Purpose != purpose || !c.Expires.After(now) {
		return nil, ErrNotFound
	}
	delete(m.ceremony, id)
	return &c, nil
}

func (m *MemoryStore) Reset(_ context.Context, uid uuid.UUID) (Removed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var r Removed
	_, r.TOTP = m.totp[uid]
	delete(m.totp, uid)
	r.RecoveryCodes = len(m.codes[uid])
	delete(m.codes, uid)
	kept := m.passkeys[:0]
	for _, p := range m.passkeys {
		if p.UserID == uid {
			r.Passkeys++
		} else {
			kept = append(kept, p)
		}
	}
	m.passkeys = kept
	return r, nil
}

func (m *MemoryStore) Factors(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]Factors, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[uuid.UUID]Factors{}
	for _, id := range ids {
		f := Factors{RecoveryLeft: m.recoveryLeft(id)}
		if r, ok := m.totp[id]; ok && r.Confirmed {
			f.TOTP = true
		}
		for _, p := range m.passkeys {
			if p.UserID == id {
				f.Passkeys++
			}
		}
		out[id] = f
	}
	return out, nil
}
