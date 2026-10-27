package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"hatamoto.dev/core/internal/adminpath"
)

// adminStore is the part of adminpath.Store the boot and the CLI use.
type adminStore interface {
	Get(ctx context.Context) (adminpath.Stored, error)
	Set(ctx context.Context, path, by string) error
	SetAck(ctx context.Context, path, by string) error
	SetIfPending(ctx context.Context, path string) (bool, error)
}

// defaultAdminAddress is what an install that already has an owner keeps
// when it has no stored address yet (an upgrade).
const defaultAdminAddress = "/admin"

// staticSite is a Site that serves nothing extra: Assess still applies the
// fixed reserved prefixes.
type staticSite struct{}

func (staticSite) Serves(context.Context, string) (bool, string) { return false, "" }
func (staticSite) Name() string                                  { return "" }
func (staticSite) Domain() string                                { return "" }

func reasonText(rs []adminpath.Reason) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = r.Text
	}
	return strings.Join(parts, "; ")
}

func hasReason(rs []adminpath.Reason, code string) bool {
	for _, r := range rs {
		if r.Code == code {
			return true
		}
	}
	return false
}

// checkStored guards a stored address that may have been edited by hand. A
// malformed one, or one equal to a fixed reserved prefix, refuses to boot; one
// that a page or redirect has since come to serve (a page or
// redirect now uses it) is kept, with a warning, so the owner is never locked
// out.
func checkStored(ctx context.Context, stored string, site adminpath.Site, logger *slog.Logger) (string, error) {
	res := adminpath.Assess(ctx, stored, site)
	if res.Level == adminpath.LevelInvalid || res.Path != stored {
		return "", fmt.Errorf("the stored admin address %q is not valid; run `hatamoto admin-url set <path>` or `hatamoto admin-url random` to replace it", stored)
	}
	if res.Level == adminpath.LevelReserved && hasReason(res.Reasons, "reserved") {
		// A fixed prefix can never become free later, unlike a page slug.
		return "", fmt.Errorf("the stored admin address %q is not usable: %s; run `hatamoto admin-url set <path>` or `hatamoto admin-url random` to replace it", stored, reasonText(res.Reasons))
	}
	if res.Level == adminpath.LevelReserved {
		why := reasonText(res.Reasons)
		logger.Warn(fmt.Sprintf("the stored admin address %s is now reserved: %s; run `hatamoto admin-url set <path>` to choose another", stored, why), "path", stored, "reason", why)
	}
	return stored, nil
}

// resolveAdminPath picks the address the admin is served at: the
// HATAMOTO_ADMIN_PATH pin, else the stored address, else (first boot) a
// random one when there is no owner yet or "/admin" when there is one. The
// pin is never written to the database.
func resolveAdminPath(ctx context.Context, st adminStore, site adminpath.Site, hasOwner func(context.Context) (bool, error), envPin string, logger *slog.Logger) (string, error) {
	if envPin != "" {
		res := adminpath.Assess(ctx, envPin, site)
		switch res.Level {
		case adminpath.LevelInvalid, adminpath.LevelReserved:
			return "", fmt.Errorf("HATAMOTO_ADMIN_PATH %q refused: %s", envPin, reasonText(res.Reasons))
		case adminpath.LevelWeak:
			why := reasonText(res.Reasons)
			logger.Warn(fmt.Sprintf("admin address %s from HATAMOTO_ADMIN_PATH is weak: %s", res.Path, why), "path", res.Path, "reason", why)
		}
		return res.Path, nil
	}

	cur, err := st.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("read admin address: %w", err)
	}
	if cur.Path != nil {
		return checkStored(ctx, *cur.Path, site, logger)
	}

	owner, err := hasOwner(ctx)
	if err != nil {
		return "", fmt.Errorf("check for an owner: %w", err)
	}
	pending := defaultAdminAddress
	if !owner {
		if pending, err = adminpath.Generate(); err != nil {
			return "", fmt.Errorf("generate admin address: %w", err)
		}
	}
	if _, err := st.SetIfPending(ctx, pending); err != nil {
		return "", err
	}
	// Re-read: another replica may have won the write.
	if cur, err = st.Get(ctx); err != nil {
		return "", fmt.Errorf("read admin address: %w", err)
	}
	if cur.Path == nil {
		return "", fmt.Errorf("admin address was not stored")
	}
	return *cur.Path, nil
}

// bootAdminPath is resolveAdminPath, except in demo mode: the demo refusals
// match the default address only, so the demo is always served at /admin. The
// pin and the stored address are not read and nothing is written.
func bootAdminPath(ctx context.Context, demo bool, st adminStore, site adminpath.Site, hasOwner func(context.Context) (bool, error), envPin string, logger *slog.Logger) (string, error) {
	if demo {
		logger.Info("demo mode: the admin is served at " + defaultAdminAddress + " (HATAMOTO_ADMIN_PATH and the stored address are not used)")
		return defaultAdminAddress, nil
	}
	return resolveAdminPath(ctx, st, site, hasOwner, envPin, logger)
}
