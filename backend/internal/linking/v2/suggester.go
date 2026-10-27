package v2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/database"
)

// ErrNotImplemented remains for callers still wired against the Phase 1 stub
// surface (NoopSuggester). Real Suggest() uses concrete error returns.
var ErrNotImplemented = errors.New("not implemented")

// Suggestion is a single proposed link the editor may accept or skip. The
// suggestion panel renders one row per Suggestion; "Insert" sends the
// (Source, Target, Anchor) tuple to POST /linking/insert.
type Suggestion struct {
	TargetEntityID   uuid.UUID `json:"target_entity_id"`
	TargetName       string    `json:"target_name"`
	TargetSchemaSlug string    `json:"target_schema_slug"`
	AnchorPhrase     string    `json:"anchor_phrase"`
	URL              string    `json:"url"`
	// Status is "insertable" when WrapAll would successfully wrap on a fresh
	// copy of the source content, else "skipped" with SkipReason populated.
	// The panel UI uses this to render an Insert button vs a greyed row with
	// the reason ("already linked to /blog/seo", "inside <code>", …).
	Status     string `json:"status"`
	SkipReason string `json:"skip_reason,omitempty"`
	Priority   int    `json:"priority"`
}

// Suggester proposes links for review without mutating content.
type Suggester interface {
	Suggest(ctx context.Context, sourceEntityID uuid.UUID) ([]Suggestion, error)
}

// NoopSuggester remains for tests / wiring that don't want a DB-backed
// implementation. Real callers (the HTTP handler) construct a DBSuggester.
type NoopSuggester struct{}

// Suggest satisfies Suggester for NoopSuggester.
func (NoopSuggester) Suggest(_ context.Context, _ uuid.UUID) ([]Suggestion, error) {
	return nil, ErrNotImplemented
}

// Suggest runs the non-mutating suggestion engine for sourceEntityID.
//
// Algorithm (ADR §4.2):
//  1. Load source's content (from attributes JSONB).
//  2. Load active hatamoto_internal_link_rules joined with their target entity's
//     slug + schema (rules are repurposed as alias hints in v2).
//  3. Load the set of targets already linked from this source via the
//     hatamoto_entity_link_index projection — exclude these from suggestions.
//  4. For each remaining rule, dry-run WrapAll against the unmodified source
//     content with that rule's anchor phrase. The single-LinkSpec call tells
//     us whether this rule would have landed had it been applied alone.
//  5. Return one Suggestion per rule with status=insertable|skipped, ordered
//     by rule priority DESC (rules already store priority).
//
// The function is read-only. Insertion happens via the separate /linking/insert
// path so the panel UX can defer the write to an explicit click.
func Suggest(ctx context.Context, db *database.DB, urls URLBuilder, sourceEntityID uuid.UUID, limit int) ([]Suggestion, error) {
	return SuggestWithContent(ctx, db, urls, sourceEntityID, limit, nil)
}

// SuggestWithContent is Suggest with an optional live-editor override for
// attributes.content. The editor's suggestion panel sends the HTML it is
// currently holding so suggestions (and the already-linked filter) reflect
// unsaved edits rather than the last persisted row. When contentOverride is
// non-nil the persisted link index is NOT consulted — WrapAllJSONB's own
// entityLinkedAnywhere guard reports already_linked from the live content,
// so a link the editor just removed becomes suggestible again immediately.
func SuggestWithContent(ctx context.Context, db *database.DB, urls URLBuilder, sourceEntityID uuid.UUID, limit int, contentOverride *string) ([]Suggestion, error) {
	if sourceEntityID == uuid.Nil {
		return nil, fmt.Errorf("source entity id is required")
	}
	// limit <= 0 now means "no cap" — the panel needs every rule's outcome
	// to render an honest "N rules scanned, M insertable" count.
	// Each per-rule dry-run is microseconds and the source content is loaded
	// once, so scanning 595 vs 50 doesn't change wall-clock noticeably.

	conn, err := db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Release()

	source, err := loadSourceForSuggest(ctx, conn, sourceEntityID)
	if err != nil {
		return nil, err
	}
	if contentOverride != nil {
		patched, err := withContent(source.attributes, *contentOverride)
		if err != nil {
			return nil, fmt.Errorf("apply content override: %w", err)
		}
		source.attributes = patched
	}
	if len(source.attributes) == 0 || string(source.attributes) == "{}" {
		return nil, nil
	}

	alreadyLinked := map[uuid.UUID]bool{}
	if contentOverride == nil {
		alreadyLinked, err = loadAlreadyLinkedTargets(ctx, conn, sourceEntityID)
		if err != nil {
			return nil, fmt.Errorf("load already-linked: %w", err)
		}
	}

	rules, err := loadActiveRules(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("load rules: %w", err)
	}

	out := make([]Suggestion, 0, len(rules))
	for _, rule := range rules {
		if rule.TargetEntityID == sourceEntityID {
			// Don't suggest self-links.
			continue
		}
		if alreadyLinked[rule.TargetEntityID] {
			continue
		}

		anchor := rule.MatchValue
		if rule.AnchorText != nil && *rule.AnchorText != "" {
			anchor = *rule.AnchorText
		}
		if anchor == "" {
			continue
		}

		canonical, err := Resolve(urls, rule.TargetSchemaSlug, rule.TargetSlug, "")
		if err != nil {
			// Excluded and unknown schemas mean the rule
			// is not a valid v2 suggestion target. Surface as skipped so the
			// UI can flag stale rules pointing at excluded schemas.
			out = append(out, Suggestion{
				TargetEntityID:   rule.TargetEntityID,
				TargetName:       rule.TargetName,
				TargetSchemaSlug: rule.TargetSchemaSlug,
				AnchorPhrase:     anchor,
				Status:           "skipped",
				SkipReason:       SkipResolverError,
				Priority:         rule.Priority,
			})
			continue
		}

		// Dry-run wrap against the FULL attributes (top-level content +
		// page_sections). Suggestions for service/industry/page sources
		// would otherwise always be "no_match" because attributes.content
		// is empty for those schemas.
		res, _, err := WrapAllJSONB(source.attributes, []LinkSpec{{
			TargetEntityID: rule.TargetEntityID,
			AnchorText:     anchor,
			CanonicalURL:   canonical,
		}})
		if err != nil {
			return nil, fmt.Errorf("dry-run wrap for rule %s: %w", rule.ID, err)
		}

		s := Suggestion{
			TargetEntityID:   rule.TargetEntityID,
			TargetName:       rule.TargetName,
			TargetSchemaSlug: rule.TargetSchemaSlug,
			AnchorPhrase:     anchor,
			URL:              canonical,
			Priority:         rule.Priority,
		}
		if res.Inserted > 0 {
			s.Status = "insertable"
		} else {
			s.Status = "skipped"
			for k := range res.Skips {
				s.SkipReason = k
				break
			}
		}
		out = append(out, s)
	}

	// Stable: priority DESC, then alphabetic by target name for determinism
	// in tests and panel rendering.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].TargetName < out[j].TargetName
	})

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// withContent returns attrs with its top-level "content" key replaced by
// content. Every other key (page_sections, …) is preserved.
func withContent(attrs []byte, content string) ([]byte, error) {
	m := map[string]any{}
	if len(attrs) > 0 {
		if err := json.Unmarshal(attrs, &m); err != nil {
			return nil, err
		}
	}
	m["content"] = content
	return json.Marshal(m)
}

type sourceForSuggest struct {
	// attributes is the full attributes JSONB. WrapAllJSONB walks both
	// top-level `content` (blog) and `page_sections[*].content.{body,...}`
	// (service/page/industry-vertical) from a single payload.
	attributes []byte
}

func loadSourceForSuggest(ctx context.Context, conn *pgxpool.Conn, id uuid.UUID) (sourceForSuggest, error) {
	var attrsRaw []byte
	err := conn.QueryRow(ctx,
		`SELECT COALESCE(attributes, '{}'::jsonb)
		   FROM hatamoto_entities WHERE id = $1`,
		id,
	).Scan(&attrsRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sourceForSuggest{}, ErrSourceNotFound
		}
		return sourceForSuggest{}, fmt.Errorf("load source: %w", err)
	}
	return sourceForSuggest{attributes: attrsRaw}, nil
}

func loadAlreadyLinkedTargets(ctx context.Context, conn *pgxpool.Conn, sourceID uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := conn.Query(ctx,
		`SELECT DISTINCT target_entity_id
		   FROM hatamoto_entity_link_index
		  WHERE source_entity_id = $1`,
		sourceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// ruleForSuggest is the rule row joined with the minimal target metadata the
// suggestion panel needs to render — name (display) and slug + schema (resolve).
type ruleForSuggest struct {
	ID               uuid.UUID
	TargetEntityID   uuid.UUID
	MatchValue       string
	AnchorText       *string
	Priority         int
	TargetName       string
	TargetSlug       string
	TargetSchemaSlug string
}

func loadActiveRules(ctx context.Context, conn *pgxpool.Conn) ([]ruleForSuggest, error) {
	rows, err := conn.Query(ctx, `
		SELECT r.id, r.target_entity_id, r.match_value, r.anchor_text, r.priority,
		       e.name, e.slug, s.slug AS schema_slug
		  FROM hatamoto_internal_link_rules r
		  JOIN hatamoto_entities           e  ON e.id = r.target_entity_id
		  JOIN hatamoto_entity_schemas     s  ON s.id = e.schema_id
		 WHERE r.is_active = true
		   AND e.status = 'published'
		 ORDER BY r.priority DESC, r.created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ruleForSuggest
	for rows.Next() {
		var r ruleForSuggest
		if err := rows.Scan(
			&r.ID, &r.TargetEntityID, &r.MatchValue, &r.AnchorText, &r.Priority,
			&r.TargetName, &r.TargetSlug, &r.TargetSchemaSlug,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
