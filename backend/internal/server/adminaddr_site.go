package server

import (
	"context"
	"net/url"
	"strings"
	"sync"

	"hatamoto.dev/core/internal/adminpath"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/redirect"
)

// adminSeg is the one fixed route prefix that is not reserved: it is the
// legacy default admin address and may be chosen as a weak address.
const adminSeg = "admin"

// Narrow views of the collaborators siteReserved reads, so tests use fakes.
type (
	slugLookup interface {
		GetBySlug(ctx context.Context, slug string) (*entity.Entity, error)
	}
	redirectPeek interface {
		Peek(ctx context.Context, path string) *redirect.Redirect
	}
	redirectLister interface {
		ListRedirects(ctx context.Context, params redirect.RedirectListParams) ([]*redirect.Redirect, int, error)
	}
	urlSnapshotter interface {
		Snapshot(ctx context.Context) (*entity.URLSnapshot, error)
	}
)

// siteReserved tells adminpath.Assess what the running site already serves.
// It implements adminpath.Site; nil collaborators are skipped.
type siteReserved struct {
	slugs     slugLookup
	redirects redirectPeek
	listed    redirectLister // nil: redirects below a segment are not checked
	urls      urlSnapshotter
	name      func(ctx context.Context) string
	domain    func(ctx context.Context) string
	apiPrefix string // PUBLIC_API_PREFIX; its first segment is served
}

var _ adminpath.Site = (*siteReserved)(nil)

// newSiteReserved wires the provider to the server's services. A typed nil
// service must not become a non-nil interface, hence the explicit checks.
func (s *Server) newSiteReserved() *siteReserved {
	sr := &siteReserved{}
	if s.cfg != nil {
		sr.apiPrefix = s.cfg.PublicSite.APIPrefix
	}
	if s.entityRepo != nil {
		sr.slugs = s.entityRepo
	}
	if s.redirectService != nil {
		sr.redirects = s.redirectService
		sr.listed = s.redirectService
	}
	if s.entityService != nil {
		sr.urls = s.entityService.URLRegistry()
	}
	if s.siteConfig != nil {
		sr.name = func(ctx context.Context) string {
			if c, err := s.siteConfig.Get(ctx); err == nil {
				return c.CompanyName
			}
			return ""
		}
	}
	sr.domain = func(ctx context.Context) string {
		base := ""
		if s.cfg != nil {
			base = strings.TrimSpace(s.cfg.PublicSite.BaseURL)
		}
		if base == "" && s.siteConfig != nil {
			if c, err := s.siteConfig.Get(ctx); err == nil {
				base = strings.TrimSpace(c.PublicBaseURL)
			}
		}
		if u, err := url.Parse(base); err == nil {
			return u.Hostname()
		}
		return ""
	}
	return sr
}

// warnIfAdminAddressServed logs when a page, redirect or route now uses the
// first segment of the admin address. The admin keeps the address (the intercept
// claims it before the mux), so the owner is never locked out, but the content
// that shares it is unreachable until one of them is changed. Boot checks the
// same with the database view; this one sees the running site.
func (s *Server) warnIfAdminAddressServed(ctx context.Context) {
	res := adminpath.Assess(ctx, s.adminPathValue(), s.newSiteReserved())
	if res.Level != adminpath.LevelReserved {
		return
	}
	var why []string
	for _, r := range res.Reasons {
		why = append(why, r.Text)
	}
	s.logger.Warn("the admin address "+res.Path+" is also used by the site ("+strings.Join(why, "; ")+"); that content is not reachable. Run `hatamoto admin-url set <path>` to choose another address, then restart",
		"path", res.Path)
}

var (
	fixedPrefixesOnce sync.Once
	fixedPrefixes     map[string]bool
)

// fixedRoutePrefixes is the set of literal first segments of every route the
// core registers, minus the admin segment. Derived once from RouteTable.
func fixedRoutePrefixes() map[string]bool {
	fixedPrefixesOnce.Do(func() {
		fixedPrefixes = map[string]bool{}
		table, err := RouteTable()
		if err != nil {
			return
		}
		for _, rt := range table {
			p := rt.Pattern
			if i := strings.IndexByte(p, ' '); i >= 0 {
				p = p[i+1:]
			}
			seg := firstSegment(p)
			if seg == "" || seg == adminSeg || strings.ContainsAny(seg, "{}") {
				continue
			}
			fixedPrefixes[seg] = true
		}
	})
	return fixedPrefixes
}

// firstSegment returns the first path segment of "/a/b" ("a"), or "".
func firstSegment(path string) string {
	path = strings.TrimPrefix(path, "/")
	if i := strings.IndexByte(path, '/'); i >= 0 {
		path = path[:i]
	}
	return path
}

func (sr *siteReserved) Serves(ctx context.Context, path string) (bool, string) {
	seg := firstSegment(path)
	if seg == "" {
		return false, ""
	}
	for _, r := range adminpath.ReservedPrefixes {
		if seg == r {
			return true, "the site serves /" + seg
		}
	}
	if fixedRoutePrefixes()[seg] {
		return true, "the site serves /" + seg
	}
	if config.CheckPrefixAgainstAdmin(sr.apiPrefix, "/"+seg) != nil {
		return true, "PUBLIC_API_PREFIX " + sr.apiPrefix + " starts with /" + seg
	}
	if sr.urls != nil {
		if snap, err := sr.urls.Snapshot(ctx); err == nil && snap != nil {
			for _, c := range snap.Configs() {
				if c.Pattern != nil && c.Pattern.FirstLiteral() == seg {
					return true, "the " + c.SchemaSlug + " URL pattern starts with /" + seg
				}
				if firstSegment(c.ArchivePath) == seg {
					return true, "the " + c.SchemaSlug + " archive is at /" + seg
				}
			}
		}
	}
	if sr.slugs != nil {
		// Any status counts; not found (or a lookup failure) means free.
		if e, err := sr.slugs.GetBySlug(ctx, seg); err == nil && e != nil {
			return true, "a page uses the slug " + seg
		}
	}
	if sr.redirects != nil && sr.redirects.Peek(ctx, "/"+seg) != nil {
		return true, "a redirect starts at /" + seg
	}
	if sr.redirectBelow(ctx, seg) {
		return true, "a redirect starts below /" + seg
	}
	return false, ""
}

// redirectListPages bounds redirectBelow: 20 pages of 200 is far beyond a
// real redirect table, and keeps a pathological one from stalling a check.
const (
	redirectListPages    = 20
	redirectListPageSize = 200
)

// redirectBelow reports whether an active redirect has a source under
// /seg/ (/shop/old-item reserves /shop). The service's search is a substring
// match, so the result is filtered to sources that start with the prefix.
func (sr *siteReserved) redirectBelow(ctx context.Context, seg string) bool {
	if sr.listed == nil {
		return false
	}
	prefix := "/" + seg + "/"
	for page := 1; page <= redirectListPages; page++ {
		rs, total, err := sr.listed.ListRedirects(ctx, redirect.RedirectListParams{Search: prefix, Page: page, PerPage: redirectListPageSize})
		if err != nil {
			return false
		}
		for _, r := range rs {
			if r.IsActive && strings.HasPrefix(strings.ToLower(r.SourcePath), prefix) {
				return true
			}
		}
		if page*redirectListPageSize >= total {
			return false
		}
	}
	return false
}

func (sr *siteReserved) Name() string {
	if sr.name == nil {
		return ""
	}
	return sr.name(context.Background())
}

func (sr *siteReserved) Domain() string {
	if sr.domain == nil {
		return ""
	}
	return sr.domain(context.Background())
}
