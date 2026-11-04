package changeset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MaxStandingPerHour is the ceiling of the hourly cap of a standing policy, and
// the cap a policy without one gets.
const MaxStandingPerHour = 200

// maxPolicyReasons bounds how many refusal reasons a verdict lists.
const maxPolicyReasons = 5

// StandingPolicy is the rule a standing approval grants a plugin: entity
// updates of the named schemas that change only the named attribute keys.
type StandingPolicy struct {
	Plugin     string
	Fields     []string
	Schemas    []string
	MaxPerHour int
}

// PolicyVerdict says whether a changeset is inside a policy. Reasons are plain
// English and list the first few refusals only.
type PolicyVerdict struct {
	OK      bool
	Reasons []string
}

func (p StandingPolicy) cap() int {
	if p.MaxPerHour <= 0 || p.MaxPerHour > MaxStandingPerHour {
		return MaxStandingPerHour
	}
	return p.MaxPerHour
}

// AutoPublishedLastHour counts the items of plugin's automatic changesets that
// were applied in the hour before now.
func (e *Engine) AutoPublishedLastHour(ctx context.Context, plugin string) (int, error) {
	return autoPublishedSince(ctx, e.DB.Pool(), plugin, e.now().Add(-time.Hour))
}

func autoPublishedSince(ctx context.Context, q rowQuerier, plugin string, since time.Time) (int, error) {
	var n int
	err := q.QueryRow(ctx, `
		SELECT count(*) FROM hatamoto_changeset_items i
		JOIN hatamoto_changesets c ON c.id = i.changeset_id
		WHERE c.plugin = $1 AND c.auto_published AND c.state = 'published'
		  AND c.published_at > $2 AND i.result = $3`,
		plugin, since, ResultApplied).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("failed to count automatic changes: %w", err)
	}
	return n, nil
}

// MarkAutoPublished records that a published changeset went out under a
// standing approval.
func (e *Engine) MarkAutoPublished(ctx context.Context, csID, approvalID int64) error {
	tag, err := e.DB.Pool().Exec(ctx, `
		UPDATE hatamoto_changesets SET auto_published = true, approval_id = $2 WHERE id = $1 AND state = $3`,
		csID, approvalID, StatePublished)
	if err != nil {
		return fmt.Errorf("failed to mark changeset automatic: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotPublished
	}
	return nil
}

// CheckPolicy judges a staged changeset against a standing policy. It never
// changes the changeset beyond classing its items, and it fails closed: any
// item or value it cannot read is a refusal. The caller publishes with
// PublishReviewed only when the verdict is OK.
func (e *Engine) CheckPolicy(ctx context.Context, csID int64, p StandingPolicy) (PolicyVerdict, error) {
	var v PolicyVerdict
	err := e.DB.WithTx(ctx, func(tx pgx.Tx) error {
		cs, err := lockChangeset(ctx, tx, csID)
		if err != nil {
			return err
		}
		if cs.State != StateStaged {
			return ErrNotStaged
		}
		if cs.Plugin != p.Plugin || p.Plugin == "" {
			v.refuse("The changeset belongs to another plugin.")
			return nil
		}
		var proposals int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM hatamoto_run_proposals WHERE run_id = $1`, cs.RunID).Scan(&proposals); err != nil {
			return fmt.Errorf("failed to read proposals: %w", err)
		}
		if proposals > 0 {
			v.refuse("The run suggests changes that need your review.")
			return nil
		}
		if cs.Items == 0 {
			v.refuse("The run changed nothing.")
			return nil
		}
		used, err := autoPublishedSince(ctx, tx, cs.Plugin, e.now().Add(-time.Hour))
		if err != nil {
			return err
		}
		if used+int(cs.Items) > p.cap() {
			v.refuse(fmt.Sprintf("It would pass %d automatic changes in an hour.", p.cap()))
			return nil
		}
		if err := e.ensureClassified(ctx, tx, csID, cs.Plugin); err != nil {
			return err
		}
		items, err := loadItems(ctx, tx, csID, false)
		if err != nil {
			return err
		}
		risks, err := loadRisks(ctx, tx, csID)
		if err != nil {
			return err
		}
		for _, it := range items {
			if len(v.Reasons) >= maxPolicyReasons {
				break
			}
			if risks[it.Seq] != string(RiskLow) {
				v.refuse(fmt.Sprintf("Change %d needs a closer look.", it.Seq))
				continue
			}
			if reason := checkStandingItem(ctx, tx, p, it); reason != "" {
				v.refuse(fmt.Sprintf("Change %d: %s", it.Seq, reason))
			}
		}
		return nil
	})
	if err != nil {
		return PolicyVerdict{}, err
	}
	v.OK = len(v.Reasons) == 0
	return v, nil
}

func (v *PolicyVerdict) refuse(reason string) { v.Reasons = append(v.Reasons, reason) }

func loadRisks(ctx context.Context, tx pgx.Tx, csID int64) (map[int]string, error) {
	rows, err := tx.Query(ctx, `SELECT seq, COALESCE(risk, '') FROM hatamoto_changeset_items WHERE changeset_id = $1`, csID)
	if err != nil {
		return nil, fmt.Errorf("failed to read risks: %w", err)
	}
	defer rows.Close()
	out := map[int]string{}
	for rows.Next() {
		var seq int
		var risk string
		if err := rows.Scan(&seq, &risk); err != nil {
			return nil, err
		}
		out[seq] = risk
	}
	return out, rows.Err()
}

// checkStandingItem returns why an item is outside the policy, or "".
func checkStandingItem(ctx context.Context, tx pgx.Tx, p StandingPolicy, it StoredItem) string {
	if it.Kind != KindEntity {
		return "only page and record updates qualify."
	}
	if it.Op != OpUpsert {
		return "deleting never qualifies."
	}
	if len(it.BlobKeys) > 0 {
		return "files never qualify."
	}
	target := it.TargetID
	if target == "" {
		var err error
		if target, err = mappedTarget(ctx, tx, Origin(p.Plugin), it.Kind, it.SourceKey); err != nil {
			return "its target could not be read."
		}
	}
	id, err := uuid.Parse(target)
	if err != nil {
		return "creating never qualifies."
	}
	var (
		origin, name, slug, locale, status, schemaSlug string
		parent                                         *string
		attrs, schema                                  []byte
	)
	err = tx.QueryRow(ctx, `
		SELECT e.origin, e.name, e.slug, COALESCE(e.locale, ''), e.status, e.parent_id::text, e.attributes, s.slug, s.schema
		FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id WHERE e.id = $1`, id).
		Scan(&origin, &name, &slug, &locale, &status, &parent, &attrs, &schemaSlug, &schema)
	if errors.Is(err, pgx.ErrNoRows) {
		return "creating never qualifies."
	}
	if err != nil {
		return "the live record could not be read."
	}
	if origin != Origin(p.Plugin) {
		return "it changes something the plugin did not create."
	}
	if !slices.Contains(p.Schemas, schemaSlug) {
		return "its type is not covered by the rule."
	}
	var pl entityPayload
	if json.Unmarshal(it.Payload, &pl) != nil {
		return "it could not be read."
	}
	liveParent := ""
	if parent != nil {
		liveParent = *parent
	}
	switch {
	case pl.Schema != schemaSlug:
		return "it changes the record type."
	case pl.Slug != slug:
		return "the address must be sent unchanged."
	case pl.Name != "" && pl.Name != name:
		return "it changes the name."
	case pl.Locale != "" && pl.Locale != locale:
		return "it changes the language."
	case pl.Status != "" && pl.Status != status:
		return "it changes the status."
	case pl.ParentKey != "" || pl.ParentID != nil && pl.ParentID.String() != liveParent:
		return "it moves the page."
	case pl.SEO != nil:
		return "it changes search settings."
	}
	return checkStandingAttributes(p, schema, attrs, pl.Attributes)
}

// checkStandingAttributes compares the attributes an update sends with the
// live ones: every difference must be a key of the policy whose schema type is
// a number, boolean, date or enum, in the old and the new value alike.
func checkStandingAttributes(p StandingPolicy, schemaDoc, liveRaw, newRaw []byte) string {
	cur, ok := decodeObject(liveRaw)
	if !ok {
		return "its current values could not be read."
	}
	next, ok := decodeObject(newRaw)
	if !ok {
		return "its values could not be read."
	}
	var props struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(schemaDoc, &props) != nil {
		return "its type could not be read."
	}
	keys := map[string]bool{}
	for k := range cur {
		keys[k] = true
	}
	for k := range next {
		keys[k] = true
	}
	for k := range keys {
		a, inCur := cur[k]
		b, inNext := next[k]
		if inCur && inNext && reflect.DeepEqual(a, b) {
			continue
		}
		if !slices.Contains(p.Fields, k) {
			return fmt.Sprintf("it changes %q, which the rule does not cover.", k)
		}
		if !inCur || !inNext {
			return fmt.Sprintf("it adds or removes %q.", k)
		}
		def, ok := props.Properties[k]
		if !ok || !safeScalar(def, a) || !safeScalar(def, b) {
			return fmt.Sprintf("%q is not a number, yes or no, date or fixed choice.", k)
		}
	}
	return ""
}

func decodeObject(raw []byte) (map[string]any, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, true
	}
	if !isJSONObject(raw) {
		return nil, false
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil, false
	}
	return m, true
}

// safeScalar reports whether v is a value of a property the policy may touch:
// the schema declares one plain type, and v is that type (and, for a fixed
// choice, one of the choices).
func safeScalar(def json.RawMessage, v any) bool {
	var d struct {
		Type   string `json:"type"`
		Format string `json:"format"`
		Enum   []any  `json:"enum"`
	}
	if json.Unmarshal(def, &d) != nil {
		return false
	}
	if len(d.Enum) > 0 {
		for _, c := range d.Enum {
			if reflect.DeepEqual(c, v) {
				return isScalar(v)
			}
		}
		return false
	}
	switch d.Type {
	case "number", "integer":
		_, ok := v.(float64)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "string":
		s, ok := v.(string)
		switch d.Format {
		case "date":
			_, err := time.Parse("2006-01-02", s)
			return ok && err == nil
		case "date-time":
			_, err := time.Parse(time.RFC3339, s)
			return ok && err == nil
		}
	}
	return false
}

func isScalar(v any) bool {
	switch v.(type) {
	case float64, bool, string:
		return true
	}
	return false
}
