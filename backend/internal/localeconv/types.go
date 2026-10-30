// Package localeconv converts a site that models regions as page trees into
// the core multi-locale model (locale per entity, translation groups, URL
// segments). It holds the plan format, the analysis report, the conversion
// record and the Service the CLI and the admin both use.
package localeconv

import (
	"errors"
	"fmt"
	"time"
)

// ErrNotImplemented is returned by service methods that are not built yet.
var ErrNotImplemented = errors.New("localeconv: not implemented")

// Issue codes. Validation codes come from Plan.Validate, the rest from the
// analysis, apply and revert steps.
const (
	CodeUnsupportedVersion   = "unsupported_version"
	CodeNeedTwoLocales       = "need_two_locales"
	CodeTooManyLocales       = "too_many_locales"
	CodeBadTag               = "bad_tag"
	CodeDuplicateTag         = "duplicate_tag"
	CodeBadSegment           = "bad_segment"
	CodeDuplicateSegment     = "duplicate_segment"
	CodeReservedSegment      = "reserved_segment"
	CodeBadRootSlug          = "bad_root_slug"
	CodeDuplicateRoot        = "duplicate_root"
	CodeRootNotFound         = "root_not_found"
	CodeRootAmbiguous        = "root_ambiguous"
	CodeUnknownSchema        = "unknown_schema"
	CodeDefaultNotListed     = "default_not_listed"
	CodeXDefaultNotListed    = "x_default_not_listed"
	CodeMissingAttribute     = "missing_attribute"
	CodeMapUnknownTag        = "map_unknown_tag"
	CodeBadFallback          = "bad_fallback"
	CodeBadGroupAttribute    = "bad_group_attribute"
	CodeBadShared            = "bad_shared"
	CodeDuplicateStrip       = "duplicate_strip_attribute"
	CodeStripNeededAttribute = "strip_needed_attribute"
	CodeBadHomeSlug          = "bad_home_slug"
	CodeBadOnConflict        = "bad_on_conflict"

	CodePathChanges         = "path_changes"
	CodeGroupTwoInLocale    = "group_two_in_locale"
	CodePublishedCollision  = "published_collision"
	CodeTriggerGuardMissing = "trigger_guard_missing"
	CodeStaleReport         = "stale_report"
	CodeAlreadyReverted     = "already_reverted"
	CodeNewerConversion     = "newer_conversion"
	CodeRevertConflict      = "revert_conflict"

	CodeAttributeWithoutHome = "attribute_without_home"
	CodeUnmatchedValue       = "unmatched_value"
	CodeHomeChildrenShadow   = "home_children_shadow"
)

// Issue is one error or warning, in the report and in validation results.
type Issue struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	EntityID string `json:"entity_id,omitempty"`
}

// BlockingError carries the issues that stopped a dry run, apply or revert.
// The CLI maps it (errors.As) to exit code 2.
type BlockingError struct {
	Issues []Issue
	Report *Report // nil when the failure came before an analysis
}

func (e *BlockingError) Error() string {
	switch len(e.Issues) {
	case 0:
		return "blocking errors"
	case 1:
		return fmt.Sprintf("blocking error: %s: %s", e.Issues[0].Code, e.Issues[0].Message)
	}
	return fmt.Sprintf("%d blocking errors, first: %s: %s", len(e.Issues), e.Issues[0].Code, e.Issues[0].Message)
}

// Report is the dry-run result; the admin renders its table from the same struct.
type Report struct {
	PlanSHA256      string  `json:"plan_sha256"`
	DataFingerprint string  `json:"data_fingerprint"`
	OK              bool    `json:"ok"`
	Errors          []Issue `json:"errors"`
	Warnings        []Issue `json:"warnings"`

	Locales           []LocaleReport    `json:"locales"`
	Shared            SharedReport      `json:"shared"`
	PathChanges       PathChanges       `json:"path_changes"`
	RenamedCollisions []RenamedRow      `json:"renamed_collisions"`
	Groups            GroupsReport      `json:"groups"`
	Overrides         OverridesReport   `json:"overrides"`
	Redirects         RedirectsReport   `json:"redirects_under_segments"`
	UnmatchedValues   []UnmatchedValue  `json:"unmatched_values"`
	SiteSettings      SiteSettingsDelta `json:"site_settings"`
}

// LocaleReport is what one locale receives.
type LocaleReport struct {
	Tag            string         `json:"tag"`
	Segment        string         `json:"segment"`
	Home           HomeReport     `json:"home"`
	CountsBySchema map[string]int `json:"counts_by_schema"`
	Total          int            `json:"total"`
}

// HomeReport is the region home of a locale and its path before and after.
type HomeReport struct {
	ID      string `json:"id"`
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
}

// SharedReport counts the entities that stay locale-neutral.
type SharedReport struct {
	CountsBySchema map[string]int `json:"counts_by_schema"`
	Total          int            `json:"total"`
}

// PathChanges lists entities whose public path would change.
type PathChanges struct {
	Count int          `json:"count"`
	Items []PathChange `json:"items"`
}

// PathChange is one entity URL before and after.
type PathChange struct {
	ID  string `json:"id"`
	Old string `json:"old"`
	New string `json:"new"`
}

// RenamedRow is a non-published row renamed to free a slug under a region home.
type RenamedRow struct {
	ID      string `json:"id"`
	Schema  string `json:"schema"`
	OldSlug string `json:"old_slug"`
	NewSlug string `json:"new_slug"`
	Reason  string `json:"reason"`
}

// GroupsReport summarises translation groups.
type GroupsReport struct {
	Formed     int             `json:"formed"`
	Singletons int             `json:"singletons"`
	Conflicts  []GroupConflict `json:"conflicts"`
}

// GroupConflict is a group value with two rows in one locale.
type GroupConflict struct {
	Value  string   `json:"value"`
	Locale string   `json:"locale"`
	IDs    []string `json:"ids"`
}

// OverridesReport describes the schema URL overrides the conversion touches.
type OverridesReport struct {
	SitemapGroupRemoved []OverrideChange `json:"sitemap_group_removed"`
	KeptKeys            []string         `json:"kept_keys"`
}

// OverrideChange identifies one override by its match.
type OverrideChange struct {
	Schema    string `json:"schema"`
	Attribute string `json:"attribute"`
	Equals    string `json:"equals"`
	Group     string `json:"group"`
}

// RedirectsReport counts redirect rules that land under a locale segment.
type RedirectsReport struct {
	Count       int      `json:"count"`
	ItemsSample []string `json:"items_sample"`
}

// UnmatchedValue is a locale attribute value no map entry or home explains.
type UnmatchedValue struct {
	Attribute string `json:"attribute"`
	Value     string `json:"value"`
	Count     int    `json:"count"`
}

// SiteSettingsDelta pairs each changed setting as [before, after].
type SiteSettingsDelta struct {
	Language     [2]string `json:"language"`
	LocalesCount [2]int    `json:"locales_count"`
	PrefixAll    [2]bool   `json:"prefix_all"`
	XDefault     [2]string `json:"x_default"`
}

// ApplyOpts are the inputs of Apply beyond the plan.
type ApplyOpts struct {
	// By is the actor stored with the record: a user id or email, "cli".
	By string
	// Fingerprint is the data_fingerprint of the dry run the admin showed.
	// Apply refuses when the data changed since. Empty skips the check (CLI).
	Fingerprint string
}

// Record is the stored result of an applied conversion.
type Record struct {
	ID        int64     `json:"id"`
	AppliedAt time.Time `json:"applied_at"`
	AppliedBy string    `json:"applied_by"`
	Status    string    `json:"status"`
	Rows      int       `json:"rows"`
}

// HistoryItem is one row of the conversion history.
type HistoryItem struct {
	ID         int64      `json:"id"`
	AppliedAt  time.Time  `json:"applied_at"`
	AppliedBy  string     `json:"applied_by"`
	Status     string     `json:"status"`
	RevertedAt *time.Time `json:"reverted_at,omitempty"`
	RevertedBy string     `json:"reverted_by,omitempty"`
	Locales    []string   `json:"locales"`
	Rows       int        `json:"rows"`
}

// RevertResult is what a revert did. Forced lists the edits a forced revert
// overwrote, Stranded the rows created since apply in a converted locale
// (left untouched).
type RevertResult struct {
	ConversionID  int64   `json:"conversion_id"`
	Restored      int     `json:"restored"`
	Forced        []Issue `json:"forced"`
	Stranded      int     `json:"stranded"`
	StrandedItems []Issue `json:"stranded_items"`
}
