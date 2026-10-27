package app

import (
	"context"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/config"
)

// sqlSite tells adminpath.Assess what a database serves, with direct SQL so
// the CLI and the boot need no running server: an entity slug (any status),
// an active redirect source (exactly /seg or any path under it), or the first literal segment of a schema's
// url_pattern or archive_path. The fixed route prefixes are covered by
// adminpath.ReservedPrefixes inside Assess.
type sqlSite struct {
	pool         *pgxpool.Pool
	name, domain string
	apiPrefix    string // PUBLIC_API_PREFIX; its first segment is served
}

func newSQLSite(pool *pgxpool.Pool, name, baseURL string) *sqlSite {
	s := &sqlSite{pool: pool, name: name}
	if u, err := url.Parse(baseURL); err == nil {
		s.domain = u.Hostname()
	}
	return s
}

// loadSQLSite reads the site name from the site config row.
func loadSQLSite(ctx context.Context, pool *pgxpool.Pool, baseURL string) *sqlSite {
	var name string
	_ = pool.QueryRow(ctx, `SELECT company_name FROM hatamoto_site_config WHERE id = TRUE`).Scan(&name)
	return newSQLSite(pool, name, baseURL)
}

func (s *sqlSite) Name() string   { return s.name }
func (s *sqlSite) Domain() string { return s.domain }

// Serves reports whether the first segment of path is taken. A lookup that
// fails counts as free: the other checks still apply.
func (s *sqlSite) Serves(ctx context.Context, path string) (bool, string) {
	seg := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)[0]
	if seg == "" {
		return false, ""
	}
	if config.CheckPrefixAgainstAdmin(s.apiPrefix, "/"+seg) != nil {
		return true, "PUBLIC_API_PREFIX " + s.apiPrefix + " starts with /" + seg
	}
	var one int
	if s.pool.QueryRow(ctx, `SELECT 1 FROM hatamoto_entities WHERE slug = $1 LIMIT 1`, seg).Scan(&one) == nil {
		return true, "the site serves /" + seg + " (a page or entry uses that address)"
	}
	if s.pool.QueryRow(ctx, `SELECT 1 FROM hatamoto_redirects WHERE is_active AND (source_path = $1 OR starts_with(source_path, $1 || '/')) LIMIT 1`, "/"+seg).Scan(&one) == nil {
		return true, "the site serves /" + seg + " (a redirect starts there)"
	}
	rows, err := s.pool.Query(ctx, `SELECT settings->>'url_pattern', settings->>'archive_path' FROM hatamoto_entity_schemas`)
	if err != nil {
		return false, ""
	}
	defer rows.Close()
	for rows.Next() {
		var pattern, archive *string
		if rows.Scan(&pattern, &archive) != nil {
			continue
		}
		for _, p := range []*string{pattern, archive} {
			if p != nil && firstLiteralSegment(*p) == seg {
				return true, "the site serves /" + seg + " (a content type URL starts there)"
			}
		}
	}
	return false, ""
}

// firstLiteralSegment is the first segment of a URL pattern when it is
// literal text, else "" ("/team/{slug}" gives "team", "/{slug}" gives "").
func firstLiteralSegment(pattern string) string {
	seg := strings.SplitN(strings.TrimPrefix(pattern, "/"), "/", 2)[0]
	if seg == "" || strings.HasPrefix(seg, "{") {
		return ""
	}
	return strings.ToLower(seg)
}
