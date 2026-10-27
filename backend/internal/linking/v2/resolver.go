// Package v2 implements the Internal Linking v2 architecture defined in
// plans/ADR-internal-linking-v2.md. It is a clean parallel namespace to the
// legacy internal/linking package — they will coexist until Phase 4 retires
// v1. Single-tenant per the Path B migration; nothing in this package takes
// a tenant ID parameter.
package v2

import (
	"errors"
	"fmt"

	"hatamoto.dev/core/internal/urlpattern"
)

// Resolver errors.
var (
	// ErrEmptyEntitySlug guards against producing pathological hrefs like "/"
	// that would render as the home page.
	ErrEmptyEntitySlug = errors.New("entity slug is required")

	// ErrSchemaNotLinkable is returned for schemas that are not standalone
	// pages: faq (rendered inline within parents), location (not published
	// as standalone), organization (singleton site identity). The picker and
	// suggester filter these out per ADR §4.1.1.
	ErrSchemaNotLinkable = errors.New("schema is not linkable")

	// ErrUnknownSchema is returned for a schema slug the URL builder does not
	// know. Resolver refuses rather than guessing — silent fallback was
	// part of why v1 leaked redirects.
	ErrUnknownSchema = errors.New("unknown schema slug")
)

// URLBuilder renders the public path of an entity from the schema URL
// patterns. It is injected so this package stays free of storage concerns.
type URLBuilder interface {
	// BuildURL renders the path for an entity of schemaSlug. attrs are the
	// entity's attributes flattened to strings (pattern overrides compare
	// against them; nil is fine). known is false when no schema matches.
	BuildURL(schemaSlug string, attrs map[string]string, v urlpattern.Vars) (path string, known bool, err error)
}

// Resolve returns the canonical relative URL for a published entity, as
// ResolveWithAttrs with no attributes and only a {parent} slug.
func Resolve(b URLBuilder, schemaSlug, entitySlug, parentSlug string) (string, error) {
	return ResolveWithAttrs(b, schemaSlug, urlpattern.Vars{Slug: entitySlug, Parent: parentSlug}, nil)
}

// ResolveWithAttrs returns the canonical relative URL for a published entity.
//
// The URL comes from the schema's URL pattern via b. faq, location and
// organization are never standalone pages and are refused.
//
// The function does no I/O of its own; b decides what is looked up.
func ResolveWithAttrs(b URLBuilder, schemaSlug string, v urlpattern.Vars, attrs map[string]string) (string, error) {
	switch schemaSlug {
	case "faq", "location", "organization":
		return "", fmt.Errorf("%w: %s", ErrSchemaNotLinkable, schemaSlug)
	}
	if v.Slug == "" {
		return "", ErrEmptyEntitySlug
	}
	path, known, err := b.BuildURL(schemaSlug, attrs, v)
	if !known {
		return "", fmt.Errorf("%w: %s", ErrUnknownSchema, schemaSlug)
	}
	if err != nil {
		return "", err
	}
	return path, nil
}
