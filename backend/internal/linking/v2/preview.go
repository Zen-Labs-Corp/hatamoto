package v2

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/database"
)

// RulePreviewMatch is one source entity whose content mentions a rule's
// phrase, with the outcome a wrap would have on it today.
//
// Status is one of:
//   - "insertable"     — the phrase appears in linkable body text and the
//     source does not yet link to the target. The suggester would offer it.
//   - "already_linked" — the source already links to the target.
//   - "skipped"        — the phrase only appears where links are never
//     inserted (headings, code); SkipReason carries the detail.
type RulePreviewMatch struct {
	EntityID   uuid.UUID `json:"entity_id"`
	EntityName string    `json:"entity_name"`
	EntitySlug string    `json:"entity_slug"`
	SchemaSlug string    `json:"schema_slug"`
	Status     string    `json:"status"`
	SkipReason string    `json:"skip_reason,omitempty"`
}

// RulePreviewResult is the response of PreviewRule.
type RulePreviewResult struct {
	// MatchCount is the number of source entities mentioning the phrase
	// (every status). InsertableCount is the subset the suggester would offer.
	MatchCount      int                `json:"match_count"`
	InsertableCount int                `json:"insertable_count"`
	LinkedCount     int                `json:"linked_count"`
	Matches         []RulePreviewMatch `json:"matches"`
}

// previewMaxMatches caps the rows returned to the admin table. Counts are
// still computed over every match.
const previewMaxMatches = 200

// PreviewRule dry-runs a rule phrase against every published entity and
// reports which ones mention it and what a wrap would do there. It is the
// backend for the admin "eye" preview and the live preview in the rule
// editor. Read-only.
//
// The target entity itself and every variant sharing its
// (schema, slug) are excluded — linking a service page to itself is never a
// suggestion. A cheap ILIKE prefilter on the attributes JSON narrows the
// candidate set before the DOM-aware WrapAllJSONB dry-run decides the
// outcome; the prefilter is skipped for phrases with characters that may be
// HTML- or JSON-escaped in storage (so it can only widen, never miss).
func PreviewRule(ctx context.Context, db *database.DB, phrase string, targetEntityID uuid.UUID) (RulePreviewResult, error) {
	res := RulePreviewResult{Matches: []RulePreviewMatch{}}
	phrase = strings.TrimSpace(phrase)
	if phrase == "" || targetEntityID == uuid.Nil {
		return res, nil
	}

	conn, err := db.GetConn(ctx)
	if err != nil {
		return res, fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Release()

	query := `
		SELECT e.id, e.name, e.slug, s.slug,
		       COALESCE(e.attributes, '{}'::jsonb)
		  FROM hatamoto_entities e
		  JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		  JOIN hatamoto_entities t ON t.id = $1
		 WHERE e.status = 'published'
		   AND NOT (e.schema_id = t.schema_id AND e.slug = t.slug)`
	args := []any{targetEntityID}
	if prefilterSafe(phrase) {
		query += ` AND e.attributes::text ILIKE '%' || $2 || '%'`
		args = append(args, phrase)
	}
	query += ` ORDER BY s.slug, e.name`

	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return res, fmt.Errorf("load preview candidates: %w", err)
	}
	defer rows.Close()

	spec := []LinkSpec{{
		TargetEntityID: targetEntityID,
		AnchorText:     phrase,
		CanonicalURL:   "#preview",
	}}
	for rows.Next() {
		var m RulePreviewMatch
		var attrs []byte
		if err := rows.Scan(&m.EntityID, &m.EntityName, &m.EntitySlug, &m.SchemaSlug, &attrs); err != nil {
			return res, fmt.Errorf("scan preview candidate: %w", err)
		}
		wrap, _, err := WrapAllJSONB(attrs, spec)
		if err != nil {
			return res, fmt.Errorf("dry-run wrap for %s: %w", m.EntityID, err)
		}
		switch {
		case wrap.Inserted > 0:
			m.Status = "insertable"
			res.InsertableCount++
		case wrap.Skips[SkipAlreadyLinked] > 0:
			m.Status = "already_linked"
			res.LinkedCount++
		case wrap.Skips[SkipInHeading] > 0:
			m.Status, m.SkipReason = "skipped", SkipInHeading
		case wrap.Skips[SkipInCode] > 0:
			m.Status, m.SkipReason = "skipped", SkipInCode
		default:
			continue // no_match: the prefilter hit a non-body field or a partial word.
		}
		res.MatchCount++
		if len(res.Matches) < previewMaxMatches {
			res.Matches = append(res.Matches, m)
		}
	}
	if err := rows.Err(); err != nil {
		return res, err
	}

	// Actionable rows first.
	rank := map[string]int{"insertable": 0, "already_linked": 1, "skipped": 2}
	sort.SliceStable(res.Matches, func(i, j int) bool {
		return rank[res.Matches[i].Status] < rank[res.Matches[j].Status]
	})
	return res, nil
}

// prefilterSafe reports whether phrase can be substring-matched against the
// raw attributes JSON without false negatives: letters, digits, spaces and
// hyphens are stored verbatim in both HTML and JSON. Anything else (&, ",
// <, ', %, _, …) may be escaped or is an ILIKE wildcard, so we scan every row.
func prefilterSafe(phrase string) bool {
	for _, r := range phrase {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-') {
			return false
		}
	}
	return true
}
