// Package designcheck holds the static design checks of the admin UI: the rules
// in docs/ADMIN_UI_RULES.md (UI-01 to UI-11, DC-01 to DC-04, copy), token and
// literal checks, retired primitives, orphan classes, and the single-purpose CSS
// guards. The tests read backend/web/admin and never import internal/admin, so
// `make design-check` runs them (with internal/admin/a11y) in seconds.
//
// Sources of truth: web/admin/css/tokens.css (values), web/admin/components.json
// (components, their owner files and contracts) and docs/ADMIN_UI_RULES.md.
// A new static design check goes here; TestDesignChecksLiveHere fails for an
// internal/admin test that reads admin CSS source.
package designcheck
