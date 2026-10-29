package server

import (
	"context"
	"slices"
	"time"

	"hatamoto.dev/sdk"
)

// sitemapFilterTimeout bounds the sitemap.urls filter chain.
const sitemapFilterTimeout = 2 * time.Second

// applySitemapFilter runs the sitemap.urls filter chain for one sitemap file
// (its group slug; "misc" for the misc sitemap) and returns urls plus the
// entries plugins added. Plugins see only the group and language and return
// additions; the host appends those whose Loc is not already in the file
// (existing entries are never changed or removed). It returns urls unchanged
// when no engine is wired or nothing was added.
//
// Callers invoke it just before marshalling, i.e. before the XML is cached
// (cacheSitemapXML). The cache is keyed by the sitemap file name (which names
// the locale on a multi-locale site), so a filter's output is served until the
// entry's TTL expires; filters must therefore be deterministic and are not
// re-run per request.
func (s *Server) applySitemapFilter(ctx context.Context, group string, urls []URL) []URL {
	return s.applySitemapFilterLang(ctx, group, "", urls)
}

// applySitemapFilterFor runs the filter for a planned file: plugins see the
// bare group and, on a multi-locale site, the locale of the file.
func (s *Server) applySitemapFilterFor(ctx context.Context, g *sitemapGroup, urls []URL) []URL {
	return s.applySitemapFilterLang(ctx, g.base, g.locale, urls)
}

func (s *Server) applySitemapFilterLang(ctx context.Context, group, language string, urls []URL) []URL {
	if s.hooks == nil {
		return urls
	}
	ctx, cancel := context.WithTimeout(ctx, sitemapFilterTimeout)
	defer cancel()

	out, ok := s.hooks.Apply(ctx, sdk.SitemapURLsF.Name, sdk.SitemapURLs{Group: group, Language: language}).(sdk.SitemapURLs)
	if !ok {
		return urls
	}
	return mergeSitemapAdditions(urls, out.Add)
}

// mergeSitemapAdditions appends add to urls, skipping an entry whose Loc is
// empty or already present.
func mergeSitemapAdditions(urls []URL, add []sdk.SitemapURL) []URL {
	if len(add) == 0 {
		return urls
	}
	seen := make(map[string]bool, len(urls)+len(add))
	for _, u := range urls {
		seen[u.Loc] = true
	}
	merged := slices.Clone(urls)
	for _, u := range fromSDKSitemapURLs(add) {
		if u.Loc == "" || seen[u.Loc] {
			continue
		}
		seen[u.Loc] = true
		merged = append(merged, u)
	}
	return merged
}

func fromSDKSitemapURLs(urls []sdk.SitemapURL) []URL {
	out := make([]URL, len(urls))
	for i, u := range urls {
		x := URL{Loc: u.Loc, LastMod: u.LastMod, ChangeFreq: u.ChangeFreq, Priority: u.Priority}
		for _, a := range u.Alternates {
			x.Links = append(x.Links, HrefLang{Rel: a.Rel, HrefLang: a.HrefLang, Href: a.Href})
		}
		out[i] = x
	}
	return out
}

// sitemapHasLinks reports whether any URL carries alternate links, which
// require the xhtml namespace declaration on the urlset.
func sitemapHasLinks(urls []URL) bool {
	for _, u := range urls {
		if len(u.Links) > 0 {
			return true
		}
	}
	return false
}
