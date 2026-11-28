package server

import (
	"context"

	"hatamoto.dev/core/internal/siteconfig"
)

// publicAPIAdmin is the admin's view of the public content API switch.
type publicAPIAdmin struct{ site *siteconfig.Store }

func (a publicAPIAdmin) Enabled(ctx context.Context) bool {
	c, err := a.site.Get(ctx)
	return err == nil && c.PublicContentAPI
}

func (a publicAPIAdmin) SetEnabled(ctx context.Context, on bool) error {
	return a.site.SetPublicContentAPI(ctx, on)
}
