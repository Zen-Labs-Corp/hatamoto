package packages

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Developer package refusals. Each is returned inside a *DeveloperError that
// carries the package and key ids, so errors.Is matches the sentinel.
var (
	// ErrDeveloperOff is a developer package refused because the owner has not
	// allowed developer packages.
	ErrDeveloperOff = errors.New("developer packages are off")
	// ErrDeveloperKeyMismatch is a package signed by another key than the one
	// the developer index lists for it.
	ErrDeveloperKeyMismatch = errors.New("the package is not signed by the key the developer index lists")
	// ErrDeveloperSignerChanged is an update of a developer package that is not
	// signed by the key that signed the installed version. Confirm never
	// bypasses it.
	ErrDeveloperSignerChanged = errors.New("the update is not signed by the developer key of the installed version")
)

// DeveloperError is a refusal of a developer package. Err is one of the
// ErrDeveloper* sentinels.
type DeveloperError struct {
	Name    string
	Version string
	// Author is the listing's author name ("" when it names none).
	Author string
	// KeyID is the key that signed the package.
	KeyID string
	// WantKeyID is the key it should have been signed by: the index's key for
	// ErrDeveloperKeyMismatch, the installed version's for ErrDeveloperSignerChanged.
	WantKeyID string
	Err       error
}

// Unwrap returns the sentinel.
func (e *DeveloperError) Unwrap() error { return e.Err }

// Error is the message shown to the owner and printed by the CLI.
func (e *DeveloperError) Error() string {
	switch {
	case errors.Is(e.Err, ErrDeveloperOff):
		signedBy := ""
		if e.Author != "" {
			signedBy = "signed by " + e.Author + ", "
		}
		return fmt.Sprintf("%s is a developer package (%skey %s, not reviewed by Hatamoto).\n"+
			"Developer packages are off on this site. An owner can turn them on in\n"+
			"Admin > Plugins > Package security > Allow developer packages.", e.Name, signedBy, e.KeyID)
	case errors.Is(e.Err, ErrDeveloperKeyMismatch):
		return fmt.Sprintf("%s is signed by key %s, but the developer index lists key %s for it. Refused.", e.Name, e.KeyID, e.WantKeyID)
	case errors.Is(e.Err, ErrDeveloperSignerChanged):
		return fmt.Sprintf("%s %s is signed by %s, not by the key that signed the installed version (%s). Refused.", e.Name, e.Version, orUnsigned(e.KeyID), orUnsigned(e.WantKeyID))
	}
	return e.Err.Error()
}

// devGrant is the proof that a package is a developer package: the index entry
// and the public key that verified it.
type devGrant struct {
	Entry Entry
	Key   ed25519.PublicKey
}

// developerCacheTTL is how long a developer index read is reused.
const developerCacheTTL = 30 * time.Second

// developerState is the Manager's cache of the developer index.
type developerState struct {
	cached *Index
	err    error
	at     time.Time
}

// DeveloperIndex returns the verified developer index, filtered so no entry
// shadows a main-registry or first-party package (FilterDeveloper). It is
// cached for 30 seconds; force skips the cache. The main index is read for the
// filter: when that read fails (and the main registry is not simply off) the
// developer index is unavailable too, so a shadowing entry can never slip
// through. A successful read applies the index's revocations. It returns
// ErrRegistryOff when the feature is off or the install is limited to
// first-party signers (the public demo).
func (m *Manager) DeveloperIndex(ctx context.Context, force bool) (*Index, error) {
	if m.DeveloperRegistry == nil || m.DeveloperRegistry.URL == "" || strings.EqualFold(m.DeveloperRegistry.URL, "off") || len(m.OnlySigners) > 0 {
		return nil, ErrRegistryOff
	}
	m.devMu.Lock()
	defer m.devMu.Unlock()
	if !force && time.Since(m.dev.at) < developerCacheTTL {
		return m.dev.cached, m.dev.err
	}
	m.dev.cached, m.dev.err = m.fetchDeveloperIndex(ctx)
	m.dev.at = time.Now()
	return m.dev.cached, m.dev.err
}

func (m *Manager) fetchDeveloperIndex(ctx context.Context) (*Index, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	dev, err := m.DeveloperRegistry.Fetch(ctx)
	if err != nil {
		return nil, err
	}
	var main *Index
	if m.Registry != nil {
		switch main, err = m.Registry.Fetch(ctx); {
		case errors.Is(err, ErrRegistryOff):
			main = nil
		case err != nil:
			return nil, fmt.Errorf("developer index: the main registry could not be read to check names: %w", err)
		}
	}
	idx := FilterDeveloper(dev, main)
	if err := m.ApplyRevocations(ctx, idx); err != nil {
		m.Logger.Error("packages: apply revocations", "error", err)
	}
	return idx, nil
}

// checkPolicy is CheckPolicy that also returns the developer grant when p is a
// developer package the owner allows. It only reads state, so Inspect may call
// it without the install lock.
func (m *Manager) checkPolicy(ctx context.Context, p *Package, src Source) (*devGrant, error) {
	if len(m.OnlySigners) > 0 && !slices.Contains(m.OnlySigners, signerOf(p)) {
		return nil, ErrSignerNotAllowed
	}
	err := m.Trust.Verify(p)
	switch {
	case err == nil:
		return nil, nil
	case errors.Is(err, ErrUntrustedKey):
		return m.developerGrant(ctx, p, err)
	case !errors.Is(err, ErrUnsigned):
		return nil, err
	case src == SourceDev:
		if !m.AllowDev {
			return nil, ErrDevDisabled
		}
		return nil, nil
	}
	cfg, err := m.store.Config(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.AllowUnsigned {
		return nil, ErrUnsignedNotAllowed
	}
	return nil, nil
}

// developerGrant decides a signed package whose key is not in the trust set:
// refused with untrusted (the original error) unless the developer index lists
// the package, under the very key that signed it, and the owner allows them.
func (m *Manager) developerGrant(ctx context.Context, p *Package, untrusted error) (*devGrant, error) {
	idx, err := m.DeveloperIndex(ctx, false)
	if errors.Is(err, ErrRegistryOff) {
		return nil, untrusted
	}
	if err != nil {
		return nil, fmt.Errorf("%w; the developer index could not be read: %v", untrusted, err)
	}
	e, ok := idx.Find(p.Kind, p.Name)
	if !ok {
		return nil, untrusted
	}
	pub, err := ParsePublicKey(e.SignerKey)
	if err != nil {
		return nil, untrusted
	}
	signed := signerOf(p)
	if KeyID(pub) != signed {
		return nil, &DeveloperError{Name: p.Name, Version: p.Version, Author: e.Author, KeyID: signed, WantKeyID: KeyID(pub), Err: ErrDeveloperKeyMismatch}
	}
	// The first-party key never vouches for a developer package.
	if IsFirstPartyKey(signed) {
		return nil, untrusted
	}
	if err := (TrustSet{signed: pub}).Verify(p); err != nil {
		return nil, err
	}
	cfg, err := m.store.Config(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.AllowDeveloper {
		return nil, &DeveloperError{Name: p.Name, Version: p.Version, Author: e.Author, KeyID: signed, Err: ErrDeveloperOff}
	}
	return &devGrant{Entry: e, Key: pub}, nil
}

// developerRowTrust is the one-key trust set a stored developer package is
// verified against: the key kept on its row, which must be the signer. It never
// reads the owner's switch or the index, so turning developer packages off
// leaves installed ones running.
func developerRowTrust(r Row) (TrustSet, error) {
	pub, err := ParsePublicKey(r.DeveloperKey)
	if err != nil || r.SignerKeyID == "" || KeyID(pub) != r.SignerKeyID {
		return nil, fmt.Errorf("%w: the stored developer key of %s does not match its signature", ErrUntrustedKey, r.Name)
	}
	return TrustSet{r.SignerKeyID: pub}, nil
}

// SetAllowDeveloper stores the owner's switch for developer packages. Turning
// it off never stops a package that is already installed.
func (m *Manager) SetAllowDeveloper(ctx context.Context, on bool) error {
	if err := m.store.SetAllowDeveloper(ctx, on); err != nil {
		return err
	}
	m.Audit(ctx, "package.allow_developer", "package_config", map[string]any{"allow": on})
	return nil
}

// developerKeyString is the base64 form of a developer key kept on the row.
func developerKeyString(k ed25519.PublicKey) string { return base64.StdEncoding.EncodeToString(k) }
