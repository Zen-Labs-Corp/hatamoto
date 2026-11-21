package server

import (
	"fmt"
	"strings"

	"hatamoto.dev/core/internal/modhost"
)

// CheckModuleRoutes refuses module route prefixes that would hide or be hidden
// by the admin address, the public API prefix or a scanner probe path. The
// caller (serve) exits on the error, like the API prefix check.
func CheckModuleRoutes(adminAddr, apiPrefix string) error {
	adminSeg := strings.ToLower(firstSegment(adminAddr))
	for _, m := range modhost.Routes() {
		bare := strings.TrimSuffix(m.Prefix, "/")
		if adminSeg != "" && adminSeg == firstSegment(bare) {
			return fmt.Errorf("module %q route prefix %s is under the admin address /%s; run `hatamoto admin-url set <path>` to choose another address",
				m.Module, m.Prefix, adminSeg)
		}
		if apiPrefix != "" && (underPrefix(bare, apiPrefix) || underPrefix(apiPrefix, bare)) {
			return fmt.Errorf("module %q route prefix %s overlaps PUBLIC_API_PREFIX %s; change the prefix or unset PUBLIC_API_PREFIX",
				m.Module, m.Prefix, apiPrefix)
		}
		// auth-legacy is the /auth/ scanner probe: /auth/ prefixes are governed by
		// modhost.RouteRefusal, and the intercept exempts a mounted module prefix.
		for _, p := range []string{bare, m.Prefix} {
			if name, probe := wellKnownName(p); probe && name != "auth-legacy" {
				return fmt.Errorf("module %q route prefix %s is a scanner probe path (%s) and would be refused; choose another prefix",
					m.Module, m.Prefix, name)
			}
		}
	}
	return nil
}
