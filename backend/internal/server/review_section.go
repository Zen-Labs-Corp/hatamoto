package server

import (
	"context"
	"fmt"
	"strings"

	"hatamoto.dev/core/internal/entity"
)

// reviewSection is Edit.Section: the first segment of e's public path without the locale prefix
// ("/pricing"), "" for the home page and for schemas without public pages. It errors when the URL
// registry cannot be read, so the caller does not mistake "unknown" for "no section".
func (s *Server) reviewSection(ctx context.Context, repo resolveRepo, e *entity.Entity) (string, error) {
	if e == nil || e.Schema == nil {
		return "", nil
	}
	snap, err := s.urlSnapshotOrErr(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to load the url registry: %w", err)
	}
	if _, ok := snap.Config(e.Schema.Slug); !ok {
		return "", nil
	}
	return sectionOf(locateEntity(ctx, snap, repo, e).Path, snap.WithLocale(e.Locale, "/")), nil
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
