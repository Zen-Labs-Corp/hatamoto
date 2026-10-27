package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/urlpattern"
)

// publicScope narrows the slug-based page loaders to the rows a resolved URL
// refers to. It is attached to the request context by resolveRouteWith.
type publicScope struct {
	// SchemaSlugs restricts the lookup to these schemas (a schema and aliases).
	SchemaSlugs []string
	// EntityID, when set, is the row the resolver matched: the page loader
	// loads it by id, because slugs are only unique among siblings.
	EntityID uuid.UUID
	// SkipRedirects is set because the resolver already checked the full path.
	SkipRedirects bool
}

type publicScopeKey struct{}

func withPublicScope(ctx context.Context, sc publicScope) context.Context {
	return context.WithValue(ctx, publicScopeKey{}, sc)
}

func publicScopeFrom(ctx context.Context) (publicScope, bool) {
	sc, ok := ctx.Value(publicScopeKey{}).(publicScope)
	return sc, ok
}

// resolveRepo is the slice of the public repository the resolver needs.
type resolveRepo interface {
	GetPublishedBySlugAndSchemas(ctx context.Context, slug string, schemaSlugs []string) (*entity.Entity, error)
	GetEntityPath(ctx context.Context, entityID uuid.UUID) ([]entity.BreadcrumbPathItem, error)
	GetPublishedByPath(ctx context.Context, segments []string, schemaSlugs []string) (*entity.Entity, error)
}

func isEntityNotFound(err error) bool { return errors.Is(err, entity.ErrEntityNotFound) }

// resolvedPage is the outcome of resolving a public URL path.
type resolvedPage struct {
	// Kind is resolveKindEntity, resolveKindHome or resolveKindArchive.
	Kind string
	// Entity is the matched row; nil for home and archive results.
	Entity *entity.Entity
	// Slug is the page slug delegated to the page handlers (entity and home).
	Slug string
	// Archive describes the matched schema archive (resolveKindArchive only).
	Archive *resolvedArchive
	// SchemaSlugs scopes the follow-up page lookup to the matched schema(s).
	SchemaSlugs []string
	// URL is the canonical path of the entity.
	URL string
}

// Resolve result kinds, exposed to clients as the payload's `type` field.
const (
	resolveKindEntity  = "entity"
	resolveKindHome    = "home"
	resolveKindArchive = "archive"
)

// resolvedArchive is a schema's archive listing matched by its archive_path.
type resolvedArchive struct {
	SchemaSlug string
	Aliases    []string
	Path       string
	// Title and Description are the resolved heading and optional meta
	// description (see archiveHeading).
	Title       string
	Description string
}

// resolveEntity maps a URL path to a published base entity using the schema
// URL patterns. It returns (nil, nil) when nothing serves the path.
func resolveEntity(ctx context.Context, repo resolveRepo, snap *entity.URLSnapshot, path string) (*resolvedPage, error) {
	router := snap.Router()

	for _, cand := range router.Resolve(path) {
		var ent *entity.Entity
		var err error
		if cand.Pattern.HasPath() {
			ent, err = repo.GetPublishedByPath(ctx, urlpattern.SplitPath(cand.Vars.Path), cand.SchemaSlugs)
		} else {
			ent, err = repo.GetPublishedBySlugAndSchemas(ctx, cand.Vars.Slug, cand.SchemaSlugs)
		}
		if err != nil {
			if isEntityNotFound(err) {
				continue
			}
			return nil, err
		}
		if ent.Schema == nil || !servesAt(router, ent, cand) {
			continue
		}
		if cand.Pattern.FirstLiteral() == "" && snap.IsSectionPage(ent.Schema.Slug, ent.Slug) {
			continue // served as a section landing page below
		}
		if cand.Pattern.HasDate() && !datesMatch(snap, ent, cand.Vars) {
			continue
		}
		if cand.Vars.Parent != "" {
			ok, err := hasParentSlug(ctx, repo, ent, cand.Vars.Parent)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		return finishResolved(ent, cand.SchemaSlugs, cand.Pattern, cand.Vars)
	}

	// Section landing pages: a page whose slug equals a neutral section prefix
	// is served at /{slug}.
	seg := strings.Trim(path, "/")
	if seg != "" && !strings.Contains(seg, "/") && snap.SectionPrefixes()[seg] {
		if schemas := snap.SectionPageSchemas(); len(schemas) > 0 {
			ent, err := repo.GetPublishedBySlugAndSchemas(ctx, seg, schemas)
			switch {
			case err == nil:
				return &resolvedPage{Kind: resolveKindEntity, Entity: ent, Slug: ent.Slug, SchemaSlugs: schemas, URL: "/" + seg}, nil
			case !isEntityNotFound(err):
				return nil, err
			}
		}
	}

	// A schema's archive_path, when no section page or entity claimed it.
	if norm := "/" + strings.Trim(path, "/"); norm != "/" {
		for _, c := range snap.Configs() {
			if c.ArchivePath != "" && strings.TrimRight(c.ArchivePath, "/") == norm {
				title, desc := archiveHeading(c, c.SchemaSlug)
				return &resolvedPage{Kind: resolveKindArchive, URL: norm, Archive: &resolvedArchive{
					SchemaSlug: c.SchemaSlug, Aliases: c.Aliases, Path: norm,
					Title: title, Description: desc,
				}}, nil
			}
		}
	}
	return nil, nil
}

// servesAt reports whether cand's pattern is the entity's canonical pattern
// (after attribute overrides), so an entity with an override is not also
// reachable at its schema's base pattern.
func servesAt(router *urlpattern.Router, ent *entity.Entity, cand urlpattern.Candidate) bool {
	target, ok := router.Lookup(ent.Schema.Slug, entity.FlattenAttributes(ent.Attributes))
	return ok && target.Pattern.String() == cand.Pattern.String()
}

func finishResolved(ent *entity.Entity, schemas []string, pat *urlpattern.Pattern, v urlpattern.Vars) (*resolvedPage, error) {
	u, err := urlpattern.Render(pat, v)
	if err != nil {
		return nil, fmt.Errorf("render resolved url: %w", err)
	}
	return &resolvedPage{Kind: resolveKindEntity, Entity: ent, Slug: ent.Slug, SchemaSlugs: schemas, URL: u}, nil
}

// datesMatch reports whether the {year}/{month}/{day} of a request are those
// of the entity's publication date, so /2024/05/x does not serve a post
// published in another month.
func datesMatch(snap *entity.URLSnapshot, ent *entity.Entity, got urlpattern.Vars) bool {
	want := snap.VarsFor(ent, nil)
	return (got.Year == "" || got.Year == want.Year) &&
		(got.Month == "" || got.Month == want.Month) &&
		(got.Day == "" || got.Day == want.Day)
}

func hasParentSlug(ctx context.Context, repo resolveRepo, ent *entity.Entity, parentSlug string) (bool, error) {
	if ent.ParentID == nil {
		return false, nil
	}
	path, err := repo.GetEntityPath(ctx, ent.ID)
	if err != nil {
		return false, err
	}
	return len(path) >= 2 && path[len(path)-2].Slug == parentSlug, nil
}
