package server

import (
	"context"
	"log/slog"
	"strings"

	"hatamoto.dev/core/internal/fonts"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/siteconfig"
)

// fontFaces returns the installed faces the page's body and heading fonts use,
// and the hash of the one to preload. A list error returns nothing and never
// fails the page.
func (s *Server) fontFaces(ctx context.Context, theme siteconfig.ThemeConfig) ([]render.FontFace, string) {
	if s.fonts == nil {
		return nil, ""
	}
	list, err := s.fonts.List(ctx)
	if err != nil {
		s.logger.Warn("render: listing fonts", slog.String("error", err.Error()))
		return nil, ""
	}
	return pickFontFaces(list, theme.Typography.FontFamily, theme.Typography.HeadingFamily)
}

// pickFontFaces selects the faces whose family is the first family of the body
// or heading stack. The raw stacks are used, not the page tokens, which drop
// a stack equal to the default: an uploaded "Inter" must still work.
func pickFontFaces(list []fonts.Font, bodyStack, headingStack string) ([]render.FontFace, string) {
	body, heading := fonts.FirstFamily(bodyStack), fonts.FirstFamily(headingStack)
	var faces []render.FontFace
	var bodyFaces []fonts.Font
	for _, f := range list {
		isBody := body != "" && strings.EqualFold(f.Family, body)
		if !isBody && !(heading != "" && strings.EqualFold(f.Family, heading)) {
			continue
		}
		faces = append(faces, render.FontFace{Family: f.Family, Weight: f.Weight, Style: f.Style, Hash: f.Hash, UnicodeRange: f.UnicodeRange})
		if isBody {
			bodyFaces = append(bodyFaces, f)
		}
	}
	return faces, preloadHash(bodyFaces)
}

// preloadHash picks the body font's regular face: normal style, the weight
// closest to 400 (the lighter on a tie), the latin subset when there is one.
func preloadHash(faces []fonts.Font) string {
	var best *fonts.Font
	dist := func(f fonts.Font) int {
		d := f.Weight - 400
		if d < 0 {
			d = -d
		}
		return d
	}
	better := func(f, b fonts.Font) bool {
		if (f.Style == "normal") != (b.Style == "normal") {
			return f.Style == "normal"
		}
		if dist(f) != dist(b) {
			return dist(f) < dist(b)
		}
		if f.Weight != b.Weight {
			return f.Weight < b.Weight
		}
		return f.Subset == "latin" && b.Subset != "latin"
	}
	for i := range faces {
		if best == nil || better(faces[i], *best) {
			best = &faces[i]
		}
	}
	if best == nil {
		return ""
	}
	return best.Hash
}
