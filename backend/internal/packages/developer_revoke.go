package packages

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DeveloperEvery is how often Run reads the developer index, which is what
// applies its revocations between admin refreshes.
const DeveloperEvery = 6 * time.Hour

// DeveloperFirstRead is the delay before Run's first read after start; up to
// the same time again is added as jitter.
const DeveloperFirstRead = 30 * time.Second

// ErrRevoked is a package the developer index has revoked: it stays disabled
// until the registry no longer lists the revocation.
var ErrRevoked = errors.New("the package was revoked by the registry")

// revocationFor returns the revocation of idx that applies to the developer
// row r: its signer key, or its exact name and version.
func revocationFor(r Row, idx *Index) (Revocation, bool) {
	if !r.Developer || idx == nil {
		return Revocation{}, false
	}
	for _, rv := range idx.Revoked {
		if rv.SignerKey != "" {
			if pub, err := ParsePublicKey(rv.SignerKey); err == nil && KeyID(pub) == r.SignerKeyID {
				return rv, true
			}
		}
		if rv.Name != "" && rv.Version != "" && rv.Name == r.Name && rv.Version == r.Version {
			return rv, true
		}
	}
	return Revocation{}, false
}

// revocationReason is the stored reason; a revocation without one still needs
// a non-empty text, because the reason marks the row as revoked.
func revocationReason(rv Revocation) string {
	if rv.Reason != "" {
		return rv.Reason
	}
	return "revoked by the registry"
}

// ApplyRevocations disables the installed developer packages idx revokes. It
// runs inside every successful developer index read. It never deletes a row
// or its data, and never touches a first-party package. A revoked theme that
// is active is deactivated.
func (m *Manager) ApplyRevocations(ctx context.Context, idx *Index) error {
	if idx == nil || len(idx.Revoked) == 0 {
		return nil
	}
	rows, err := m.store.List(ctx)
	if err != nil {
		return err
	}
	cfg, err := m.store.Config(ctx)
	if err != nil {
		return err
	}
	var errs []error
	changed := false
	for _, r := range rows {
		if r.DisabledReason != "" {
			continue
		}
		activeTheme := r.Kind == KindTheme && r.Name == cfg.ActiveTheme
		live := r.Enabled || activeTheme
		rv, ok := revocationFor(r, idx)
		if !ok {
			continue
		}
		reason := revocationReason(rv)
		if err := m.store.DisableWithReason(ctx, r.Kind, r.Name, reason); err != nil {
			errs = append(errs, err)
			continue
		}
		if activeTheme {
			if err := m.store.SetActiveTheme(ctx, ""); err != nil {
				errs = append(errs, err)
			}
		}
		if !live {
			// Already off: only the reason is stored, so it cannot be switched back on.
			continue
		}
		changed = true
		attr := "plugin"
		if r.Kind == KindTheme {
			attr = "theme"
		}
		m.Logger.Warn("developer package disabled: revoked by the registry", attr, r.Name, "key", r.SignerKeyID, "reason", reason)
		m.Audit(WithActor(ctx, Actor{Email: "registry"}), "package.revoke_disable", string(r.Kind)+":"+r.Name,
			map[string]any{"key": r.SignerKeyID, "reason": reason, "version": r.Version})
	}
	if changed {
		m.Reconcile(ctx)
	}
	return errors.Join(errs...)
}

// checkNotRevoked guards turning a package back on. A row without a stored
// reason is fine unless it is a developer package the cached developer index
// revokes. One with a reason needs a fresh developer index that no
// longer revokes it; otherwise (or when the index cannot be read) it returns
// ErrRevoked. A package whose row is missing is left to the caller's own error.
func (m *Manager) checkNotRevoked(ctx context.Context, kind Kind, name string) error {
	row, err := m.store.Get(ctx, kind, name)
	if err != nil || (row.DisabledReason == "" && !row.Developer) {
		return nil
	}
	if row.DisabledReason == "" {
		// Not revoked as far as is known: the cached index decides, and an
		// unreadable one does not keep a working package off.
		idx, err := m.DeveloperIndex(ctx, false)
		if err != nil {
			return nil
		}
		if rv, ok := revocationFor(row, idx); ok {
			return fmt.Errorf("%s: %w: %s", name, ErrRevoked, revocationReason(rv))
		}
		return nil
	}
	idx, err := m.DeveloperIndex(ctx, true)
	if err != nil {
		return fmt.Errorf("%s: %w: %s; the registry could not be read to check again: %v", name, ErrRevoked, row.DisabledReason, err)
	}
	if rv, ok := revocationFor(row, idx); ok {
		return fmt.Errorf("%s: %w: %s", name, ErrRevoked, revocationReason(rv))
	}
	if kind == KindTheme {
		// Activate only sets the active theme; lift the stored reason here.
		return m.store.SetEnabled(ctx, kind, name, true)
	}
	return nil
}
