package packages

import (
	"context"
	"errors"
	"fmt"

	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

var (
	ErrCommercialNotEntitled = errors.New("commercial plugin not entitled on this site")
	ErrCommercialSigner      = errors.New("commercial plugin is not signed by this site's plugin key")
)

// isCommercial reports whether p is a plugin that declares license "commercial".
func isCommercial(p *Package) bool {
	if p.Kind != KindPlugin {
		return false
	}
	man, err := p.PluginManifest()
	return err == nil && man.License == sdk.LicenseCommercial
}

// commercialPolicy decides a commercial plugin alone: no developer index,
// unsigned opt-in or dev source.
func (m *Manager) commercialPolicy(ctx context.Context, p *Package, src Source) error {
	if src == SourceDev || len(m.PluginKey) == 0 {
		return fmt.Errorf("%s: %w", p.Name, ErrCommercialNotEntitled)
	}
	if err := (TrustSet{KeyID(m.PluginKey): m.PluginKey}).Verify(p); err != nil {
		return fmt.Errorf("%s: %w", p.Name, ErrCommercialSigner)
	}
	if m.Entitled == nil || !m.Entitled(ctx, p.Name) {
		return fmt.Errorf("%s: %w", p.Name, ErrCommercialNotEntitled)
	}
	man, err := p.PluginManifest()
	if err != nil {
		return err
	}
	if err := pluginhost.CommercialRangeRefusal(man); err != nil {
		return &ParseError{Msg: err.Error()}
	}
	return nil
}

// commercialUsable reports whether r is a commercial plugin row (handled) and
// why it must not run. A plugin row whose manifest does not decode is refused,
// never treated as free.
func (m *Manager) commercialUsable(ctx context.Context, r Row) (bool, error) {
	if r.Kind != KindPlugin {
		return false, nil
	}
	var man sdk.Manifest
	if err := decodeManifest(r.Manifest, &man); err != nil {
		return true, err
	}
	if man.License != sdk.LicenseCommercial {
		return false, nil
	}
	switch {
	case r.Source == SourceDev, r.Developer, r.SignerKeyID == "", len(m.PluginKey) == 0:
		return true, fmt.Errorf("%s: %w", r.Name, ErrCommercialNotEntitled)
	case r.SignerKeyID != KeyID(m.PluginKey):
		return true, fmt.Errorf("%s: %w", r.Name, ErrCommercialSigner)
	case m.Entitled == nil || !m.Entitled(ctx, r.Name):
		return true, fmt.Errorf("%s: %w", r.Name, ErrCommercialNotEntitled)
	}
	return true, nil
}
