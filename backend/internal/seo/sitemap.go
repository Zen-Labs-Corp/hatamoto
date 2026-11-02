package seo

import (
	"fmt"
	"strings"
)

// Limits on the sitemap settings.
const (
	MaxSitemapURLs         = 50000
	MaxSitemapExcludePaths = 100
	MaxSitemapExcludeLen   = 200
)

// SitemapChangeFreqs lists the valid change frequencies; "" keeps the default.
var SitemapChangeFreqs = []string{"", "always", "hourly", "daily", "weekly", "monthly", "yearly", "never"}

// Sitemap holds the site-wide sitemap controls. The zero value reproduces the
// historic output, so default-on options are stored inverted.
type Sitemap struct {
	NoAuthor          bool     `json:"no_author,omitempty"` // leave author pages out
	Taxonomy          bool     `json:"taxonomy,omitempty"`  // add taxonomy sitemaps
	Images            bool     `json:"images,omitempty"`    // add image entries
	MaxURLs           int      `json:"max_urls,omitempty"`  // 0: MaxSitemapURLs
	NoStats           bool     `json:"no_stats,omitempty"`  // omit priority and changefreq
	DefaultPriority   *float64 `json:"default_priority,omitempty"`
	DefaultChangeFreq string   `json:"default_changefreq,omitempty"`
	ExcludePaths      []string `json:"exclude_paths,omitempty"`
}

// IsZero reports whether no sitemap setting is saved.
func (s Sitemap) IsZero() bool {
	return !s.NoAuthor && !s.Taxonomy && !s.Images && s.MaxURLs == 0 && !s.NoStats &&
		s.DefaultPriority == nil && s.DefaultChangeFreq == "" && len(s.ExcludePaths) == 0
}

// EffectiveMaxURLs is the per-file URL cap. Safe on a nil receiver.
func (s *Sitemap) EffectiveMaxURLs() int {
	if s == nil || s.MaxURLs <= 0 || s.MaxURLs > MaxSitemapURLs {
		return MaxSitemapURLs
	}
	return s.MaxURLs
}

// ExcludedPath reports whether path matches an exclusion pattern: an exact
// path, or a prefix when the pattern ends in "*". A trailing slash is ignored.
// Safe on a nil receiver.
func (s *Sitemap) ExcludedPath(path string) bool {
	if s == nil || len(s.ExcludePaths) == 0 {
		return false
	}
	path = trimSlash(path)
	for _, pat := range s.ExcludePaths {
		if prefix, ok := strings.CutSuffix(pat, "*"); ok {
			if strings.HasPrefix(path, prefix) || path+"/" == prefix {
				return true
			}
			continue
		}
		if trimSlash(pat) == path {
			return true
		}
	}
	return false
}

func trimSlash(p string) string {
	if len(p) > 1 {
		return strings.TrimRight(p, "/")
	}
	return p
}

// validateSitemap checks the sitemap key.
func validateSitemap(s Sitemap) []FieldError {
	var errs []FieldError
	if s.MaxURLs < 0 || s.MaxURLs > MaxSitemapURLs {
		errs = append(errs, FieldError{"sitemap[max_urls]", fmt.Sprintf("Enter a number from 1 to %d.", MaxSitemapURLs)})
	}
	if p := s.DefaultPriority; p != nil && (*p < 0 || *p > 1) {
		errs = append(errs, FieldError{"sitemap[default_priority]", "Priority is a number from 0 to 1."})
	}
	if !containsString(SitemapChangeFreqs, s.DefaultChangeFreq) {
		errs = append(errs, FieldError{"sitemap[default_changefreq]", "Choose one of the listed frequencies."})
	}
	if len(s.ExcludePaths) > MaxSitemapExcludePaths {
		errs = append(errs, FieldError{"sitemap[exclude_paths]", fmt.Sprintf("At most %d paths.", MaxSitemapExcludePaths)})
	}
	seen := map[string]bool{}
	for _, p := range s.ExcludePaths {
		if msg := excludePathProblem(p); msg != "" {
			errs = append(errs, FieldError{"sitemap[exclude_paths]", p + ": " + msg})
		} else if seen[p] {
			errs = append(errs, FieldError{"sitemap[exclude_paths]", p + ": listed twice."})
		}
		seen[p] = true
	}
	return errs
}

func excludePathProblem(p string) string {
	switch {
	case len(p) > MaxSitemapExcludeLen:
		return fmt.Sprintf("at most %d characters.", MaxSitemapExcludeLen)
	case !strings.HasPrefix(p, "/"):
		return "start with /."
	case strings.ContainsAny(p, " \t\r\n?#") || strings.Contains(p, ".."):
		return "use a plain site path."
	case strings.Count(p, "*") > 1 || (strings.Contains(p, "*") && !strings.HasSuffix(p, "*")):
		return "* is only allowed as the last character."
	}
	return ""
}
