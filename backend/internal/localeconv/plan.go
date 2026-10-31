package localeconv

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/taxonomy"
)

const (
	planVersion       = 1
	minLocales        = 2
	maxLocales        = 32
	maxSegmentLen     = 32
	defaultRootSchema = "page"
	defaultHomeSlug   = "home"
	FallbackRoot      = "root"
	FallbackShared    = "shared"
	OnConflictRename  = "rename-archived"
	OnConflictAbort   = "abort"
	sharedLocale      = entity.LocaleShared
)

var segmentRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// extraReservedSegments are routes the site serves that taxonomy.ReservedSegment
// does not cover. Kept in step with internal/server (which localeconv must not
// import); the server's own list is the source of truth.
var extraReservedSegments = []string{"category", "tag", "plugin-ui"}

// ReservedSegmentReason says why seg is a route of its own, or "".
func ReservedSegmentReason(seg string) string {
	if taxonomy.ReservedSegment(seg) || slices.Contains(extraReservedSegments, seg) {
		return "a route of its own"
	}
	if strings.HasPrefix(seg, "sitemap") {
		return "reserved for a route of its own"
	}
	return ""
}

// Plan is the conversion plan. The JSON field names are the plan file format.
type Plan struct {
	Version         int          `json:"version"`
	Locales         []LocaleSpec `json:"locales"`
	RootSchema      string       `json:"root_schema"`
	Default         string       `json:"default"`
	XDefault        string       `json:"x_default"`
	PrefixAll       *bool        `json:"prefix_all"`
	LocaleFrom      LocaleFrom   `json:"locale_from"`
	GroupFrom       *GroupFrom   `json:"group_from,omitempty"`
	Shared          string       `json:"shared"`
	StripAttributes []string     `json:"strip_attributes"`
	Home            HomeSpec     `json:"home"`
}

// LocaleSpec is one locale: its tag, URL segment and the top-level page that
// roots its tree.
type LocaleSpec struct {
	Tag      string `json:"tag"`
	Segment  string `json:"segment"`
	RootSlug string `json:"root_slug"`
}

// LocaleFrom says how an entity's locale is read from its attributes.
type LocaleFrom struct {
	Attribute string            `json:"attribute"`
	Map       map[string]string `json:"map"`
	Fallback  string            `json:"fallback"`
}

// GroupFrom names the attribute whose shared value links translations.
type GroupFrom struct {
	Attribute string `json:"attribute"`
}

// HomeSpec says how a region home is named and what happens to a slug clash.
type HomeSpec struct {
	Slug       string `json:"slug"`
	OnConflict string `json:"on_conflict"`
}

// PrefixAllValue is PrefixAll with its default (true) applied.
func (p *Plan) PrefixAllValue() bool { return p.PrefixAll == nil || *p.PrefixAll }

// StripsLocaleAttribute reports whether strip_attributes removes the locale
// attribute. The analysis uses it to raise strip_needed_attribute when a
// schema override still matches on that attribute.
func (p *Plan) StripsLocaleAttribute() bool {
	return slices.Contains(p.StripAttributes, p.LocaleFrom.Attribute)
}

// ParsePlan reads a plan strictly (unknown fields and trailing data are
// errors) and applies the defaults. It does not validate; call Validate.
func ParsePlan(r io.Reader) (*Plan, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var p Plan
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("parse plan: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("parse plan: unexpected data after the plan")
	}
	p.applyDefaults()
	return &p, nil
}

func (p *Plan) applyDefaults() {
	if p.Version == 0 {
		p.Version = planVersion
	}
	if p.RootSchema == "" {
		p.RootSchema = defaultRootSchema
	}
	if p.PrefixAll == nil {
		t := true
		p.PrefixAll = &t
	}
	if p.LocaleFrom.Map == nil {
		p.LocaleFrom.Map = map[string]string{}
	}
	if p.LocaleFrom.Fallback == "" {
		p.LocaleFrom.Fallback = FallbackRoot
	}
	if p.Shared == "" {
		p.Shared = sharedLocale
	}
	if p.StripAttributes == nil {
		p.StripAttributes = []string{}
	}
	if p.Home.Slug == "" {
		p.Home.Slug = defaultHomeSlug
	}
	if p.Home.OnConflict == "" {
		p.Home.OnConflict = OnConflictRename
	}
}

// Validate checks everything that needs no database. root_slug existence and
// the schema name are checked against the data by the analysis. Tags and the
// default are compared after normalising.
func (p *Plan) Validate() []Issue {
	var out []Issue
	add := func(code, format string, args ...any) {
		out = append(out, Issue{Code: code, Message: fmt.Sprintf(format, args...)})
	}

	if p.Version != planVersion {
		add(CodeUnsupportedVersion, "plan version %d is not supported, use %d", p.Version, planVersion)
	}
	switch n := len(p.Locales); {
	case n < minLocales:
		add(CodeNeedTwoLocales, "the plan lists %d locales, at least %d are needed", n, minLocales)
	case n > maxLocales:
		add(CodeTooManyLocales, "the plan lists %d locales, at most %d are allowed", n, maxLocales)
	}

	tags := map[string]bool{}
	segs := map[string]string{}
	roots := map[string]string{}
	for i, l := range p.Locales {
		tag, err := entity.NormalizeLocale(l.Tag)
		switch {
		case err != nil:
			add(CodeBadTag, "locales[%d]: %q is not a BCP 47 language tag", i, l.Tag)
		case tags[tag]:
			add(CodeDuplicateTag, "locales[%d]: tag %q is listed twice", i, tag)
		default:
			tags[tag] = true
		}

		switch {
		case l.Segment == "" || len(l.Segment) > maxSegmentLen || !segmentRe.MatchString(l.Segment):
			add(CodeBadSegment, "locales[%d]: segment %q must be lowercase letters, digits and single hyphens, at most %d characters", i, l.Segment, maxSegmentLen)
		case segs[l.Segment] != "":
			add(CodeDuplicateSegment, "locales[%d]: segment %q is used twice", i, l.Segment)
		default:
			segs[l.Segment] = l.Tag
			if reason := ReservedSegmentReason(l.Segment); reason != "" {
				add(CodeReservedSegment, "locales[%d]: segment %q is %s", i, l.Segment, reason)
			}
		}

		switch {
		case !validSlug(l.RootSlug):
			add(CodeBadRootSlug, "locales[%d]: root_slug %q is not a valid slug", i, l.RootSlug)
		case roots[l.RootSlug] != "":
			add(CodeDuplicateRoot, "locales[%d]: root_slug %q is used twice", i, l.RootSlug)
		default:
			roots[l.RootSlug] = l.Tag
		}
	}

	if !tags[normalized(p.Default)] {
		add(CodeDefaultNotListed, "default %q is not one of the listed tags", p.Default)
	}
	if p.XDefault != "" && !tags[normalized(p.XDefault)] {
		add(CodeXDefaultNotListed, "x_default %q is not one of the listed tags", p.XDefault)
	}

	if p.LocaleFrom.Attribute == "" {
		add(CodeMissingAttribute, "locale_from.attribute is required")
	}
	for _, value := range sortedKeys(p.LocaleFrom.Map) {
		if !tags[normalized(p.LocaleFrom.Map[value])] {
			add(CodeMapUnknownTag, "locale_from.map[%q] is %q, which is not a listed tag", value, p.LocaleFrom.Map[value])
		}
	}
	if p.LocaleFrom.Fallback != FallbackRoot && p.LocaleFrom.Fallback != FallbackShared {
		add(CodeBadFallback, "locale_from.fallback %q must be %q or %q", p.LocaleFrom.Fallback, FallbackRoot, FallbackShared)
	}
	if p.GroupFrom != nil && p.GroupFrom.Attribute == "" {
		add(CodeBadGroupAttribute, "group_from.attribute must not be empty")
	}
	if p.Shared != sharedLocale {
		add(CodeBadShared, "shared %q must be %q", p.Shared, sharedLocale)
	}

	seen := map[string]bool{}
	for _, a := range p.StripAttributes {
		if seen[a] {
			add(CodeDuplicateStrip, "strip_attributes lists %q twice", a)
		}
		seen[a] = true
	}

	if !validSlug(p.Home.Slug) {
		add(CodeBadHomeSlug, "home.slug %q is not a valid slug", p.Home.Slug)
	}
	if p.Home.OnConflict != OnConflictRename && p.Home.OnConflict != OnConflictAbort {
		add(CodeBadOnConflict, "home.on_conflict %q must be %q or %q", p.Home.OnConflict, OnConflictRename, OnConflictAbort)
	}
	return out
}

func validSlug(s string) bool { return s != "" && segmentRe.MatchString(s) }

func normalized(tag string) string {
	n, err := entity.NormalizeLocale(tag)
	if err != nil {
		return ""
	}
	return n
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
