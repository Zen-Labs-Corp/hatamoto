package v2

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/database"
)

// EquityTargetRow is one row in the equity dashboard — a single target
// entity with its aggregate inbound link counts across all sources.
//
// ADR plans/ADR-internal-linking-v2.md §4.5. The dashboard reads exclusively
// from hatamoto_entity_link_index (single indexed scan, ~ms), never re-walks
// JSONB.
type EquityTargetRow struct {
	TargetEntityID uuid.UUID `json:"target_entity_id"`
	TargetName     string    `json:"target_name"`
	TargetSlug     string    `json:"target_slug"`
	TargetSchema   string    `json:"target_schema"`
	// InboundTotal is the total number of <a> wrappers pointing at this
	// target across every source entity.
	InboundTotal int `json:"inbound_total"`
	// InboundBySourceSchema breaks the total down by the schema of the
	// linking source (blog, service, page, …) so the dashboard can answer
	// "where are these links coming from?" without a second query.
	InboundBySourceSchema map[string]int `json:"inbound_by_source_schema"`
	// Tier is derived from the max priority of any active manual rule
	// targeting this entity: 100→"primary", 90→"secondary", 70-89→"cluster",
	// else "". Drives the pillar dashboard filter and badges on the equity
	// page. The hierarchy itself lives in hatamoto_internal_link_rules.priority
	// so adding a new pillar is a single UPDATE, not a code change.
	Tier string `json:"tier"`
}

// EquityReport is the full payload for /admin/settings/linking/equity. The
// targets slice is sorted by inbound total descending so the dashboard
// shows hotspots first.
type EquityReport struct {
	Targets []EquityTargetRow `json:"targets"`
	// LinksTotal is the total row count in hatamoto_entity_link_index — a
	// quick sanity number for the dashboard header.
	LinksTotal int `json:"links_total"`
	// UniqueSources is the number of distinct source entities that contain
	// any inbound link.
	UniqueSources int `json:"unique_sources"`
}

// ComputeEquity aggregates hatamoto_entity_link_index by target. Single query
// using JSON aggregation so the per-source-schema breakdown lands in
// the same round trip. No N+1.
func ComputeEquity(ctx context.Context, db *database.DB) (*EquityReport, error) {
	conn, err := db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	// Per-target aggregation. The breakdown map comes from jsonb_object_agg
	// over a CTE that pre-groups by source_schema.
	const q = `
		WITH per_source AS (
			SELECT eli.target_entity_id,
			       src_schema.slug AS source_schema_slug,
			       COUNT(*)::int AS n
			  FROM hatamoto_entity_link_index eli
			  JOIN hatamoto_entities src ON src.id = eli.source_entity_id
			  JOIN hatamoto_entity_schemas src_schema ON src_schema.id = src.schema_id
			 GROUP BY eli.target_entity_id, src_schema.slug
		),
		by_schema AS (
			SELECT target_entity_id,
			       jsonb_object_agg(source_schema_slug, schema_total) AS schemas
			  FROM (
			    SELECT target_entity_id, source_schema_slug, SUM(n)::int AS schema_total
			      FROM per_source
			     GROUP BY target_entity_id, source_schema_slug
			  ) t
			 GROUP BY target_entity_id
		),
		totals AS (
			SELECT target_entity_id, SUM(n)::int AS total
			  FROM per_source
			 GROUP BY target_entity_id
		),
		tier_priority AS (
			SELECT target_entity_id, MAX(priority) AS max_priority
			  FROM hatamoto_internal_link_rules
			 WHERE source = 'manual' AND is_active = true
			 GROUP BY target_entity_id
		)
		SELECT t.target_entity_id,
		       tgt.name,
		       tgt.slug,
		       tgt_schema.slug AS target_schema,
		       t.total,
		       by_schema.schemas,
		       tp.max_priority
		  FROM totals t
		  JOIN by_schema ON by_schema.target_entity_id = t.target_entity_id
		  JOIN hatamoto_entities tgt ON tgt.id = t.target_entity_id
		  JOIN hatamoto_entity_schemas tgt_schema ON tgt_schema.id = tgt.schema_id
		  LEFT JOIN tier_priority tp ON tp.target_entity_id = t.target_entity_id
		 ORDER BY t.total DESC, tgt.name ASC
	`

	rows, err := conn.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("equity aggregate: %w", err)
	}
	defer rows.Close()

	report := &EquityReport{Targets: make([]EquityTargetRow, 0, 64)}
	for rows.Next() {
		var r EquityTargetRow
		var schemasJSON []byte
		var maxPriority *int
		if err := rows.Scan(
			&r.TargetEntityID,
			&r.TargetName,
			&r.TargetSlug,
			&r.TargetSchema,
			&r.InboundTotal,
			&schemasJSON,
			&maxPriority,
		); err != nil {
			return nil, fmt.Errorf("equity scan: %w", err)
		}
		r.Tier = tierForPriority(maxPriority)
		if err := json.Unmarshal(schemasJSON, &r.InboundBySourceSchema); err != nil {
			r.InboundBySourceSchema = map[string]int{}
		}
		report.Targets = append(report.Targets, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("equity rows: %w", err)
	}

	// Lightweight totals for the dashboard header. Same connection, two
	// extra cheap queries on the indexed table.
	if err := conn.QueryRow(ctx,
		`SELECT COUNT(*)::int FROM hatamoto_entity_link_index`,
	).Scan(&report.LinksTotal); err != nil {
		return nil, fmt.Errorf("count links: %w", err)
	}
	if err := conn.QueryRow(ctx,
		`SELECT COUNT(DISTINCT source_entity_id)::int FROM hatamoto_entity_link_index`,
	).Scan(&report.UniqueSources); err != nil {
		return nil, fmt.Errorf("count sources: %w", err)
	}

	return report, nil
}

// tierForPriority maps a rule's max priority to a tier label. The
// thresholds match the manual rule curation (priority 100 = primary
// pillar, 90 = secondary, 70-89 = cluster); see the prod rule corpus
// in hatamoto_internal_link_rules.
func tierForPriority(p *int) string {
	if p == nil {
		return ""
	}
	switch {
	case *p >= 100:
		return "primary"
	case *p >= 90:
		return "secondary"
	case *p >= 70:
		return "cluster"
	default:
		return ""
	}
}

// EquityDetailRow is one inbound link, joined with its source entity's
// display fields, surfaced when the dashboard drills into a target.
type EquityDetailRow struct {
	SourceEntityID  uuid.UUID `json:"source_entity_id"`
	SourceName      string    `json:"source_name"`
	SourceSlug      string    `json:"source_slug"`
	SourceSchema    string    `json:"source_schema"`
	AnchorText      string    `json:"anchor_text"`
	Href            string    `json:"href"`
	OccurrenceIndex int       `json:"occurrence_index"`
}

// ListInboundLinks returns every source-side row pointing at targetID.
// Drill-down view from the equity dashboard. Sorted by source name so the
// list reads stably between refreshes.
func ListInboundLinks(ctx context.Context, db *database.DB, targetID uuid.UUID) ([]EquityDetailRow, error) {
	conn, err := db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	const q = `
		SELECT src.id, src.name, src.slug, src_schema.slug AS source_schema,
		       eli.anchor_text, eli.href, eli.occurrence_index
		  FROM hatamoto_entity_link_index eli
		  JOIN hatamoto_entities src ON src.id = eli.source_entity_id
		  JOIN hatamoto_entity_schemas src_schema ON src_schema.id = src.schema_id
		 WHERE eli.target_entity_id = $1
		 ORDER BY src.name ASC, eli.occurrence_index ASC
	`
	rows, err := conn.Query(ctx, q, targetID)
	if err != nil {
		return nil, fmt.Errorf("inbound list: %w", err)
	}
	defer rows.Close()

	var out []EquityDetailRow
	for rows.Next() {
		var r EquityDetailRow
		if err := rows.Scan(
			&r.SourceEntityID, &r.SourceName, &r.SourceSlug, &r.SourceSchema,
			&r.AnchorText, &r.Href, &r.OccurrenceIndex,
		); err != nil {
			return nil, fmt.Errorf("scan inbound: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
