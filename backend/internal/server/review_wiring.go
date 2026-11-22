package server

import (
	"context"
	"strings"

	"hatamoto.dev/core/internal/reviewflow"
)

// setupReview builds the review flow on the changeset engine and installs it as
// the entity service's review gate. With the site's review switch off (the
// default) and no review policy registered, the gate holds nothing.
func (s *Server) setupReview() {
	if s.changesets == nil || s.entityService == nil || s.siteConfig == nil {
		return
	}
	f := &reviewflow.Flow{
		Switch:   s.siteConfig,
		Engine:   s.changesets,
		Entities: s.entityService,
		Users:    s.userRepo,
		Site:     s.reviewSite,
		Logger:   s.logger,
	}
	if s.emailService != nil {
		f.Mail = s.emailService
	}
	s.reviewFlow = f
	s.entityService.SetReviewGate(f)
}

// reviewSite names the site and gives the admin address that mail links start
// from. The address comes from the configured site address, never a request.
func (s *Server) reviewSite(ctx context.Context) (name, adminURL string) {
	name, base, adminPath := s.digestSite(ctx)
	return name, strings.TrimRight(base, "/") + adminPath
}
