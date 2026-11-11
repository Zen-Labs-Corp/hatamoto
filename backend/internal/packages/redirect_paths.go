package packages

import (
	"errors"
	"fmt"
	"slices"

	"hatamoto.dev/sdk"
)

// ErrRedirectPathTaken is returned when a plugin declares a request.redirect
// path another installed plugin already declares.
var ErrRedirectPathTaken = errors.New("package: redirect path already declared by another plugin")

// redirectPathConflict returns ErrRedirectPathTaken when p declares a
// redirect_paths entry that another plugin among rows declares. Disabled
// plugins count, so enabling one never surprises; a package being removed does
// not.
func redirectPathConflict(rows []Row, p *Package) error {
	if p.Kind != KindPlugin {
		return nil
	}
	man, err := p.PluginManifest()
	if err != nil || len(man.RedirectPaths) == 0 {
		return nil
	}
	for _, r := range rows {
		if r.Kind != KindPlugin || r.Name == p.Name || r.State == StateUninstalling {
			continue
		}
		var other sdk.Manifest
		if decodeManifest(r.Manifest, &other) != nil {
			continue
		}
		for _, path := range man.RedirectPaths {
			if slices.Contains(other.RedirectPaths, path) {
				return fmt.Errorf("%w: %q is declared by plugin %q", ErrRedirectPathTaken, path, r.Name)
			}
		}
	}
	return nil
}
