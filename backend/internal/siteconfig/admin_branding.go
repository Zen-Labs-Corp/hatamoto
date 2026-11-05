package siteconfig

import (
	"context"
	"fmt"
	"strings"

	"hatamoto.dev/core/internal/database"
)

// AdminBrand is how the admin and its sign-in page present themselves. Empty
// fields mean unset. Validation lives in the admin package.
type AdminBrand struct {
	Name        string
	LogoURL     string
	Accent      string
	ManagedText string
	ManagedURL  string
}

// SetAdminBranding stores the admin branding in one statement, apart from the
// positional Update SQL. Values are trimmed.
func (s *Store) SetAdminBranding(ctx context.Context, b AdminBrand) error {
	conn, err := s.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return fmt.Errorf("acquire db conn: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `
		UPDATE hatamoto_site_config
		SET admin_brand_name = $1, admin_logo_url = $2, admin_accent = $3,
		    admin_managed_by_text = $4, admin_managed_by_url = $5, updated_at = NOW()
		WHERE id = TRUE`,
		strings.TrimSpace(b.Name), strings.TrimSpace(b.LogoURL), strings.TrimSpace(b.Accent),
		strings.TrimSpace(b.ManagedText), strings.TrimSpace(b.ManagedURL)); err != nil {
		return fmt.Errorf("set admin branding: %w", err)
	}
	s.Invalidate()
	return nil
}

// ResetAdminBranding clears all five branding fields.
func (s *Store) ResetAdminBranding(ctx context.Context) error {
	return s.SetAdminBranding(ctx, AdminBrand{})
}
