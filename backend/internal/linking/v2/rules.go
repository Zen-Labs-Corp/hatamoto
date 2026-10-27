package v2

// Alias rules: the editor's "phrase X links to entity Y" table
// (hatamoto_internal_link_rules). The suggester and equity tiers read it;
// this file owns its CRUD. There is no injection engine behind it.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/database"
)

// RuleType defines how a linking rule matches content.
//
// Only keyword rules exist. The legacy "tag" and "schema_affinity" types
// were never read by the v2 suggester (it matches the phrase in content
// regardless of type), so they were removed to stop implying behaviour that
// does not exist. The rule_type column stays for schema compatibility and
// always holds "keyword".
type RuleType string

const (
	RuleTypeKeyword RuleType = "keyword"
)

// RuleSourceManual identifies a rule that was created by hand through
// the admin UI (as opposed to one of the auto-generation strategies that
// existed before migration 093). Persisted in the source column so
// "delete by source" can target legacy auto-generated cohorts.
const RuleSourceManual = "manual"

// LinkRule represents a reusable rule for generating internal links.
type LinkRule struct {
	ID                uuid.UUID `json:"id"`
	TargetEntityID    uuid.UUID `json:"target_entity_id"`
	RuleType          RuleType  `json:"rule_type"`
	MatchValue        string    `json:"match_value"`
	Priority          int       `json:"priority"`
	MaxLinksPerSource int       `json:"max_links_per_source"`
	IsActive          bool      `json:"is_active"`
	AnchorText        *string   `json:"anchor_text,omitempty"`
	Source            string    `json:"source"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`

	// Joined fields
	TargetEntityName string `json:"target_entity_name,omitempty"`
	TargetEntitySlug string `json:"target_entity_slug,omitempty"`
	TargetSchemaSlug string `json:"target_schema_slug,omitempty"`
	LinkCount        int    `json:"link_count"` // Number of active v2 links pointing at the target
}

// CreateRuleInput represents input for creating a new linking rule.
type CreateRuleInput struct {
	TargetEntityID    uuid.UUID `json:"target_entity_id"`
	RuleType          RuleType  `json:"rule_type"`
	MatchValue        string    `json:"match_value"`
	Priority          int       `json:"priority"`
	MaxLinksPerSource int       `json:"max_links_per_source"`
	IsActive          bool      `json:"is_active"`
	AnchorText        *string   `json:"anchor_text,omitempty"`
	Source            string    `json:"source,omitempty"`
}

// UpdateRuleInput represents input for updating a linking rule.
//
// AnchorText: nil leaves it unchanged; "" clears it (the suggester then
// falls back to match_value).
type UpdateRuleInput struct {
	TargetEntityID    *uuid.UUID `json:"target_entity_id,omitempty"`
	MatchValue        *string    `json:"match_value,omitempty"`
	Priority          *int       `json:"priority,omitempty"`
	MaxLinksPerSource *int       `json:"max_links_per_source,omitempty"`
	IsActive          *bool      `json:"is_active,omitempty"`
	AnchorText        *string    `json:"anchor_text,omitempty"`
}

// RuleFilter filters rules in list queries with optional pagination.
type RuleFilter struct {
	RuleType       RuleType   `json:"rule_type,omitempty"`
	IsActive       *bool      `json:"is_active,omitempty"`
	Source         string     `json:"source,omitempty"`
	Query          string     `json:"q"`
	TargetEntityID *uuid.UUID `json:"target_entity_id"`
	Page           int        `json:"page"`
	PerPage        int        `json:"per_page"`
	Sort           string     `json:"sort"`
	Order          string     `json:"order"`
}

// PaginatedRulesResult wraps a page of rules with pagination metadata.
type PaginatedRulesResult struct {
	Rules      []*LinkRule `json:"rules"`
	Total      int         `json:"total"`
	Page       int         `json:"page"`
	PerPage    int         `json:"per_page"`
	TotalPages int         `json:"total_pages"`
}

// Common errors.
var (
	ErrRuleNotFound = errors.New("link rule not found")
	// ErrDuplicateRule is returned when (target_entity_id, rule_type,
	// match_value) already exists — the table's unique constraint.
	ErrDuplicateRule = errors.New("a rule with this phrase already exists for this target")
)

// isUniqueViolation reports whether err is a Postgres unique_violation.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// Validation errors surfaced by the rule writers.
var (
	ErrMatchValueRequired   = errors.New("match_value is required")
	ErrTargetEntityRequired = errors.New("target_entity_id is required")
	ErrSourceRequired       = errors.New("source is required")
)

// CreateRule creates a new linking rule.
func CreateRule(ctx context.Context, db *database.DB, input *CreateRuleInput) (*LinkRule, error) {
	input.MatchValue = strings.TrimSpace(input.MatchValue)
	if input.MatchValue == "" {
		return nil, ErrMatchValueRequired
	}
	if input.TargetEntityID == uuid.Nil {
		return nil, ErrTargetEntityRequired
	}
	if input.RuleType == "" {
		input.RuleType = RuleTypeKeyword
	}
	if input.RuleType != RuleTypeKeyword {
		return nil, fmt.Errorf("invalid rule_type: %s", input.RuleType)
	}

	conn, err := db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	rule := &LinkRule{
		ID:                uuid.New(),
		TargetEntityID:    input.TargetEntityID,
		RuleType:          input.RuleType,
		MatchValue:        input.MatchValue,
		Priority:          input.Priority,
		MaxLinksPerSource: input.MaxLinksPerSource,
		IsActive:          input.IsActive,
		AnchorText:        input.AnchorText,
		Source:            input.Source,
	}

	if rule.Priority == 0 {
		rule.Priority = 50
	}
	if rule.MaxLinksPerSource == 0 {
		rule.MaxLinksPerSource = 1
	}
	if rule.Source == "" {
		rule.Source = RuleSourceManual
	}

	query := `
		INSERT INTO hatamoto_internal_link_rules (
			id, target_entity_id, rule_type, match_value,
			priority, max_links_per_source, is_active, anchor_text, source
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING created_at, updated_at
	`

	err = conn.QueryRow(ctx, query,
		rule.ID, rule.TargetEntityID, rule.RuleType, rule.MatchValue,
		rule.Priority, rule.MaxLinksPerSource, rule.IsActive, rule.AnchorText, rule.Source,
	).Scan(&rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDuplicateRule
		}
		return nil, fmt.Errorf("failed to create link rule: %w", err)
	}

	return rule, nil
}

// UpdateRule updates an existing linking rule.
func UpdateRule(ctx context.Context, db *database.DB, id uuid.UUID, input *UpdateRuleInput) (*LinkRule, error) {
	if input.MatchValue != nil {
		v := strings.TrimSpace(*input.MatchValue)
		if v == "" {
			return nil, ErrMatchValueRequired
		}
		input.MatchValue = &v
	}
	if input.TargetEntityID != nil && *input.TargetEntityID == uuid.Nil {
		return nil, ErrTargetEntityRequired
	}
	conn, err := db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	// Build dynamic SET clause
	setParts := []string{"updated_at = NOW()"}
	args := []any{}
	argIdx := 1

	if input.TargetEntityID != nil {
		setParts = append(setParts, fmt.Sprintf("target_entity_id = $%d", argIdx))
		args = append(args, *input.TargetEntityID)
		argIdx++
	}
	if input.MatchValue != nil {
		setParts = append(setParts, fmt.Sprintf("match_value = $%d", argIdx))
		args = append(args, *input.MatchValue)
		argIdx++
	}
	if input.Priority != nil {
		setParts = append(setParts, fmt.Sprintf("priority = $%d", argIdx))
		args = append(args, *input.Priority)
		argIdx++
	}
	if input.MaxLinksPerSource != nil {
		setParts = append(setParts, fmt.Sprintf("max_links_per_source = $%d", argIdx))
		args = append(args, *input.MaxLinksPerSource)
		argIdx++
	}
	if input.IsActive != nil {
		setParts = append(setParts, fmt.Sprintf("is_active = $%d", argIdx))
		args = append(args, *input.IsActive)
		argIdx++
	}
	if input.AnchorText != nil {
		setParts = append(setParts, fmt.Sprintf("anchor_text = NULLIF(BTRIM($%d), '')", argIdx))
		args = append(args, *input.AnchorText)
		argIdx++
	}

	args = append(args, id)
	query := fmt.Sprintf(`
		UPDATE hatamoto_internal_link_rules
		SET %s
		WHERE id = $%d
		RETURNING id, target_entity_id, rule_type, match_value,
		          priority, max_links_per_source, is_active, anchor_text,
		          COALESCE(source, 'manual'), created_at, updated_at
	`, strings.Join(setParts, ", "), argIdx)

	rule := &LinkRule{}
	err = conn.QueryRow(ctx, query, args...).Scan(
		&rule.ID, &rule.TargetEntityID, &rule.RuleType, &rule.MatchValue,
		&rule.Priority, &rule.MaxLinksPerSource, &rule.IsActive, &rule.AnchorText,
		&rule.Source, &rule.CreatedAt, &rule.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRuleNotFound
		}
		if isUniqueViolation(err) {
			return nil, ErrDuplicateRule
		}
		return nil, fmt.Errorf("failed to update link rule: %w", err)
	}

	return rule, nil
}

// DeleteRule removes a linking rule.
func DeleteRule(ctx context.Context, db *database.DB, id uuid.UUID) error {
	conn, err := db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	tag, err := conn.Exec(ctx, "DELETE FROM hatamoto_internal_link_rules WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to delete link rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRuleNotFound
	}
	return nil
}

// ListRulesPaginated returns linking rules with pagination, search, and sorting.
func ListRulesPaginated(ctx context.Context, db *database.DB, filter *RuleFilter) (*PaginatedRulesResult, error) {
	conn, err := db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	// Build dynamic WHERE clause
	whereClauses := []string{"1=1"}
	args := []any{}
	argIdx := 1

	if filter != nil {
		if filter.RuleType != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("r.rule_type = $%d", argIdx))
			args = append(args, filter.RuleType)
			argIdx++
		}
		if filter.IsActive != nil {
			whereClauses = append(whereClauses, fmt.Sprintf("r.is_active = $%d", argIdx))
			args = append(args, *filter.IsActive)
			argIdx++
		}
		if filter.Query != "" {
			whereClauses = append(whereClauses, fmt.Sprintf(
				"(LOWER(r.match_value) LIKE LOWER('%%' || $%d || '%%') OR LOWER(COALESCE(e.name, '')) LIKE LOWER('%%' || $%d || '%%'))",
				argIdx, argIdx,
			))
			args = append(args, filter.Query)
			argIdx++
		}
		if filter.TargetEntityID != nil {
			whereClauses = append(whereClauses, fmt.Sprintf("r.target_entity_id = $%d", argIdx))
			args = append(args, *filter.TargetEntityID)
			argIdx++
		}
		if filter.Source != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("COALESCE(r.source, 'manual') = $%d", argIdx))
			args = append(args, filter.Source)
		}
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	// Count total matching rules
	countQuery := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM hatamoto_internal_link_rules r
		LEFT JOIN hatamoto_entities e ON r.target_entity_id = e.id
		WHERE %s
	`, whereSQL)

	var total int
	if err := conn.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count rules: %w", err)
	}

	// Pagination defaults
	page := 1
	perPage := 50
	if filter != nil {
		if filter.Page > 0 {
			page = filter.Page
		}
		if filter.PerPage > 0 {
			perPage = min(filter.PerPage, 200)
		}
	}

	totalPages := 0
	if total > 0 {
		totalPages = (total + perPage - 1) / perPage
	}
	offset := (page - 1) * perPage

	// Build ORDER BY clause
	orderBy := "r.created_at DESC"
	if filter != nil && filter.Sort != "" {
		sortCol := ""
		switch filter.Sort {
		case "priority":
			sortCol = "r.priority"
		case "created_at":
			sortCol = "r.created_at"
		case "match_value":
			sortCol = "r.match_value"
		case "link_count":
			sortCol = "COALESCE(lc.link_count, 0)"
		}
		if sortCol != "" {
			dir := "DESC"
			if filter.Order == "asc" {
				dir = "ASC"
			}
			orderBy = fmt.Sprintf("%s %s", sortCol, dir)
		}
	}

	// Fetch paginated rules. link_count is the target's total inbound v2
	// link count (sourced from hatamoto_entity_link_index, populated by
	// the indexer on every entity save). Multiple rules pointing at the
	// same target will show the same count — the signal is "how popular
	// is this target", which is what the editor cares about when curating
	// rules. Pre-aggregated via a CTE so the join is a single index scan,
	// not N subqueries.
	dataQuery := fmt.Sprintf(`
		SELECT r.id, r.target_entity_id, r.rule_type, r.match_value,
		       r.priority, r.max_links_per_source, r.is_active, r.anchor_text,
		       COALESCE(r.source, 'manual'), r.created_at, r.updated_at,
		       COALESCE(e.name, ''), COALESCE(e.slug, ''), COALESCE(s.slug, ''),
		       COALESCE(lc.link_count, 0)
		FROM hatamoto_internal_link_rules r
		LEFT JOIN hatamoto_entities e ON r.target_entity_id = e.id
		LEFT JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
		LEFT JOIN (
			SELECT target_entity_id, COUNT(*)::int AS link_count
			FROM hatamoto_entity_link_index
			GROUP BY target_entity_id
		) lc ON lc.target_entity_id = r.target_entity_id
		WHERE %s
		ORDER BY %s
		LIMIT %d OFFSET %d
	`, whereSQL, orderBy, perPage, offset)

	rows, err := conn.Query(ctx, dataQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list paginated rules: %w", err)
	}
	defer rows.Close()

	var rules []*LinkRule
	for rows.Next() {
		var rule LinkRule
		if err := rows.Scan(
			&rule.ID, &rule.TargetEntityID, &rule.RuleType, &rule.MatchValue,
			&rule.Priority, &rule.MaxLinksPerSource, &rule.IsActive, &rule.AnchorText,
			&rule.Source, &rule.CreatedAt, &rule.UpdatedAt,
			&rule.TargetEntityName, &rule.TargetEntitySlug, &rule.TargetSchemaSlug,
			&rule.LinkCount,
		); err != nil {
			return nil, fmt.Errorf("failed to scan paginated rule: %w", err)
		}
		rules = append(rules, &rule)
	}

	if rules == nil {
		rules = []*LinkRule{}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &PaginatedRulesResult{
		Rules:      rules,
		Total:      total,
		Page:       page,
		PerPage:    perPage,
		TotalPages: totalPages,
	}, nil
}

// GetRule returns a single rule by ID.
func GetRule(ctx context.Context, db *database.DB, id uuid.UUID) (*LinkRule, error) {
	conn, err := db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	rule := &LinkRule{}
	err = conn.QueryRow(ctx, `
		SELECT id, target_entity_id, rule_type, match_value,
		       priority, max_links_per_source, is_active, anchor_text,
		       COALESCE(source, 'manual'), created_at, updated_at
		FROM hatamoto_internal_link_rules
		WHERE id = $1
	`, id).Scan(
		&rule.ID, &rule.TargetEntityID, &rule.RuleType, &rule.MatchValue,
		&rule.Priority, &rule.MaxLinksPerSource, &rule.IsActive, &rule.AnchorText,
		&rule.Source, &rule.CreatedAt, &rule.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRuleNotFound
		}
		return nil, fmt.Errorf("failed to get link rule: %w", err)
	}

	return rule, nil
}

// DeleteAllRules removes every linking rule from the site and returns the
// number deleted. Used by the admin "clean slate" toolbar action.
func DeleteAllRules(ctx context.Context, db *database.DB) (int, error) {
	conn, err := db.GetConn(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	tag, err := conn.Exec(ctx, "DELETE FROM hatamoto_internal_link_rules WHERE TRUE")
	if err != nil {
		return 0, fmt.Errorf("failed to delete all rules: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// DeleteRulesByIDs removes a specific set of rules by ID and returns the
// number deleted. Missing IDs are silently ignored.
func DeleteRulesByIDs(ctx context.Context, db *database.DB, ids []uuid.UUID) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	conn, err := db.GetConn(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	tag, err := conn.Exec(ctx, "DELETE FROM hatamoto_internal_link_rules WHERE id = ANY($1)", ids)
	if err != nil {
		return 0, fmt.Errorf("failed to delete rules by ids: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// DeleteRulesBySource removes every rule whose source column matches the
// given value. Used to clean up legacy auto-generated rule cohorts in
// one click.
func DeleteRulesBySource(ctx context.Context, db *database.DB, source string) (int, error) {
	if source == "" {
		return 0, ErrSourceRequired
	}
	conn, err := db.GetConn(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	tag, err := conn.Exec(ctx,
		"DELETE FROM hatamoto_internal_link_rules WHERE COALESCE(source, 'manual') = $1",
		source,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to delete rules by source: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
