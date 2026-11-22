package server

import (
	"context"
	"strings"

	"hatamoto.dev/core/internal/entity"
)

// reviewSection is Edit.Section: the first segment of e's public path without the locale prefix
// ("/pricing"), "" for the home page and for schemas without public pages.
func (s *Server) reviewSection(ctx context.Context, repo resolveRepo, e *entity.Entity) string {
	if e == nil || e.Schema == nil {
		return ""
	}
	snap := s.urlSnapshot(ctx)
	if _, ok := snap.Config(e.Schema.Slug); !ok {
		return ""
	}
	return sectionOf(locateEntity(ctx, snap, repo, e).Path, snap.WithLocale(e.Locale, "/"))
}

// sectionOf strips localeRoot ("/fr"; "/" = none) from path and returns "/" + its first segment.
func sectionOf(path, localeRoot string) string {
	if localeRoot != "" && localeRoot != "/" {
		switch {
		case path == localeRoot:
			path = "/"
		case strings.HasPrefix(path, localeRoot+"/"):
			path = path[len(localeRoot):]
		}
	}
	seg, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if seg == "" {
		return ""
	}
	return "/" + seg
}
