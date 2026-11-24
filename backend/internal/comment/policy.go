package comment

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/jsonx"
)

// Policy is a content type's community comment policy, stored in the entity
// schema's settings.comments.policy. The zero value is off: every accessor
// then returns the site value, so an unset policy changes nothing.
type Policy struct {
	Enabled           bool `json:"enabled"`
	MaxDepth          int  `json:"max_depth"`           // 1..32, default 10
	EditWindowMinutes int  `json:"edit_window_minutes"` // 1..1440, default 15
	SlowModeSeconds   int  `json:"slow_mode_seconds"`   // 0 = off, max 86400
}

// Policy bounds and defaults.
const (
	PolicyDepthDefault  = 10
	PolicyDepthMax      = 32
	PolicyWindowDefault = 15
	PolicyWindowMax     = 1440
	PolicySlowMax       = 86400
)

// Order is a content type's own thread order, stored in settings.comments.order.
type Order string

// Thread orders. "" means the site sort; OrderRank is applied by a plugin.
const (
	OrderNewest Order = "newest"
	OrderOldest Order = "oldest"
	OrderRank   Order = "rank"
)

// ValidOrder reports whether o is "" (site default) or a known order.
func ValidOrder(o string) bool {
	switch Order(o) {
	case "", OrderNewest, OrderOldest, OrderRank:
		return true
	}
	return false
}

// DefaultPolicy is the policy a type starts from when it is turned on.
func DefaultPolicy() Policy {
	return Policy{MaxDepth: PolicyDepthDefault, EditWindowMinutes: PolicyWindowDefault}
}

// DecodePolicy reads a stored policy object. It never fails: junk, a missing
// object or a non-bool enabled is off; an out-of-range or non-integer field
// is that field's default.
func DecodePolicy(raw []byte) Policy {
	p := DefaultPolicy()
	if len(raw) == 0 {
		return p
	}
	var m map[string]any
	if jsonx.Unmarshal(raw, &m, jsonx.StoredLoose) != nil {
		return p
	}
	p.Enabled, _ = m["enabled"].(bool)
	p.MaxDepth = intIn(m["max_depth"], 1, PolicyDepthMax, p.MaxDepth)
	p.EditWindowMinutes = intIn(m["edit_window_minutes"], 1, PolicyWindowMax, p.EditWindowMinutes)
	p.SlowModeSeconds = intIn(m["slow_mode_seconds"], 0, PolicySlowMax, 0)
	return p
}

// intIn returns v when it is an integer in lo..hi, else def.
func intIn(v any, lo, hi, def int) int {
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) || f < float64(lo) || f > float64(hi) {
		return def
	}
	return int(f)
}

// Validate reports the first out-of-range field, for writes.
func (p Policy) Validate() error {
	bad := func(field, msg string) error { return &FieldError{Field: field, Message: msg} }
	switch {
	case p.MaxDepth < 1 || p.MaxDepth > PolicyDepthMax:
		return bad("policy.max_depth", "must be between 1 and 32")
	case p.EditWindowMinutes < 1 || p.EditWindowMinutes > PolicyWindowMax:
		return bad("policy.edit_window_minutes", "must be between 1 and 1440")
	case p.SlowModeSeconds < 0 || p.SlowModeSeconds > PolicySlowMax:
		return bad("policy.slow_mode_seconds", "must be between 0 and 86400")
	}
	return nil
}

// Depth is the reply depth limit: the policy's when on, else the site's.
func (p Policy) Depth(site int) int {
	if !p.Enabled {
		return site
	}
	return p.MaxDepth
}

// EditWindow is how long a commenter may edit or delete their own comment.
func (p Policy) EditWindow() time.Duration {
	if !p.Enabled {
		return EditWindow
	}
	return time.Duration(p.EditWindowMinutes) * time.Minute
}

// SlowMode is the gap required between one person's comments; 0 = off.
func (p Policy) SlowMode(siteSeconds int) time.Duration {
	if !p.Enabled {
		return time.Duration(siteSeconds) * time.Second
	}
	return time.Duration(p.SlowModeSeconds) * time.Second
}

// MergeSchemaComments rewrites settings.comments.order (when order is not nil;
// "" deletes the key) and settings.comments.policy (when p is not nil) in a
// schema settings document, keeping every other key.
func MergeSchemaComments(raw []byte, order *string, p *Policy) ([]byte, error) {
	m := map[string]any{}
	if len(raw) > 0 {
		if err := jsonx.Unmarshal(raw, &m, jsonx.StoredLoose); err != nil {
			return nil, err
		}
		if m == nil {
			m = map[string]any{}
		}
	}
	cm, _ := m["comments"].(map[string]any)
	if cm == nil {
		cm = map[string]any{}
	}
	if order != nil {
		if *order == "" {
			delete(cm, "order")
		} else {
			cm["order"] = *order
		}
	}
	if p != nil {
		cm["policy"] = map[string]any{
			"enabled":             p.Enabled,
			"max_depth":           p.MaxDepth,
			"edit_window_minutes": p.EditWindowMinutes,
			"slow_mode_seconds":   p.SlowModeSeconds,
		}
	}
	if len(cm) == 0 {
		delete(m, "comments")
	} else {
		m["comments"] = cm
	}
	return jsonx.Marshal(m)
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// schemaComments reads one entity's schema settings.comments (order and
// policy); for write transactions. An unknown entity is ErrNotFound.
func schemaComments(ctx context.Context, q querier, entityID uuid.UUID) (Order, Policy, error) {
	var order *string
	var raw []byte
	err := q.QueryRow(ctx, `SELECT s.settings->'comments'->>'order', s.settings->'comments'->'policy'
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		WHERE e.id = $1`, entityID).Scan(&order, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", Policy{}, ErrNotFound
	}
	if err != nil {
		return "", Policy{}, err
	}
	o := Order(deref(order))
	if !ValidOrder(string(o)) {
		o = ""
	}
	return o, DecodePolicy(raw), nil
}
