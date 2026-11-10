package changeset

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/jsonx"
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
	// Create, when set, lets the rule publish new entries of its schemas.
	// Nil keeps the rule update-only.
	Create *CreatePolicy
}

// MaxStandingCreatesPerHour is the ceiling of the hourly create cap.
const MaxStandingCreatesPerHour = 100

// CreatePolicy is the opt-in part of a standing policy that publishes new
// entries of the named schemas, at most MaxPerHour an hour.
type CreatePolicy struct {
	Schemas    []string
	MaxPerHour int
}

// createCap is how many creates an hour the policy allows. There is no
// default: a missing or non-positive limit allows none.
func (p StandingPolicy) createCap() int {
	if p.Create == nil || p.Create.MaxPerHour <= 0 {
		return 0
	}
	return min(p.Create.MaxPerHour, MaxStandingCreatesPerHour)
}

// createsEnabled reports whether any create can qualify under the policy.
func (p StandingPolicy) createsEnabled() bool {
	return p.createCap() > 0 && len(p.Create.Schemas) > 0
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
	return countApplied(ctx, q, plugin, since, "")
}

// appliedCreate matches the items a publish applied as a new entity: a create
// leaves no pre-image and no token of a live object (publishItem records both
// for every update).
const appliedCreate = `(i.kind = 'entity' AND i.op = 'upsert' AND i.before IS NULL AND i.before_token IS NULL)`

// updatesSince counts the applied items of plugin's automatic changesets
// that are not creates: the budget of the update rule.
func updatesSince(ctx context.Context, q rowQuerier, plugin string, since time.Time) (int, error) {
	return countApplied(ctx, q, plugin, since, ` AND NOT `+appliedCreate)
}

// createsSince counts the applied creates of plugin's automatic changesets.
func createsSince(ctx context.Context, q rowQuerier, plugin string, since time.Time) (int, error) {
	return countApplied(ctx, q, plugin, since, ` AND `+appliedCreate)
}

func countApplied(ctx context.Context, q rowQuerier, plugin string, since time.Time, filter string) (int, error) {
	var n int
	err := q.QueryRow(ctx, `
		SELECT count(*) FROM hatamoto_changeset_items i
		JOIN hatamoto_changesets c ON c.id = i.changeset_id
		WHERE c.plugin = $1 AND c.auto_published AND c.state = 'published'
		  AND c.published_at > $2 AND i.result = $3`+filter,
		plugin, since, ResultApplied).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("failed to count automatic changes: %w", err)
	}
	return n, nil
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
		return e.checkPolicyIn(ctx, tx, cs, p, &v)
	})
	if err != nil {
		return PolicyVerdict{}, err
	}
	v.OK = len(v.Reasons) == 0
	return v, nil
}

// PublishUnderPolicy publishes a staged changeset only when it is inside the
// standing policy, all in one transaction under a per-plugin lock: the hourly
// cap is counted, the policy checked, the changes applied and the changeset
// marked automatic (with approvalID) atomically, so two runs of one plugin
// can never both pass a cap that only one fits. A refusal returns the verdict
// with OK false, publishes nothing and leaves the changeset staged.
func (e *Engine) PublishUnderPolicy(ctx context.Context, csID int64, p StandingPolicy, approvalID int64) (PolicyVerdict, PublishResult, error) {
	var v PolicyVerdict
	res, dropped, err := e.publish(ctx, csID, publishReviewed, func(tx pgx.Tx, cs changesetRow) error {
		if p.Plugin == "" || cs.Plugin != p.Plugin {
			v.refuse("The changeset belongs to another plugin.")
			return errPolicyRefused
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "standing:"+p.Plugin); err != nil {
			return fmt.Errorf("failed to lock plugin: %w", err)
		}
		if err := e.checkPolicyIn(ctx, tx, cs, p, &v); err != nil {
			return err
		}
		if len(v.Reasons) > 0 {
			return errPolicyRefused
		}
		return nil
	}, approvalID)
	if errors.Is(err, errPolicyRefused) {
		return v, PublishResult{}, nil
	}
	if err != nil {
		return PolicyVerdict{}, PublishResult{}, err
	}
	v.OK = true
	return v, PublishResult{Result: res, Dropped: dropped}, nil
}

// errPolicyRefused rolls a PublishUnderPolicy transaction back.
var errPolicyRefused = errors.New("changeset: outside the standing policy")

// checkPolicyIn is CheckPolicy inside the caller's transaction, with the
// changeset already locked and staged.
func (e *Engine) checkPolicyIn(ctx context.Context, tx pgx.Tx, cs changesetRow, p StandingPolicy, v *PolicyVerdict) error {
	csID := cs.ID
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
	if !p.createsEnabled() {
		used, err := autoPublishedSince(ctx, tx, cs.Plugin, e.now().Add(-time.Hour))
		if err != nil {
			return err
		}
		if used+int(cs.Items) > p.cap() {
			v.refuse(fmt.Sprintf("It would pass %d automatic changes in an hour.", p.cap()))
			return nil
		}
	}
	if err := e.ensureClassified(ctx, tx, csID, cs.Plugin); err != nil {
		return err
	}
	items, err := loadItems(ctx, tx, csID, false)
	if err != nil {
		return err
	}
	st := &standingRun{created: map[string]string{}, slugs: map[string]bool{}}
	if p.createsEnabled() {
		staged := 0
		for _, it := range items {
			if isCleanCreate(ctx, tx, p, it) {
				staged++
			}
		}
		used, err := updatesSince(ctx, tx, cs.Plugin, e.now().Add(-time.Hour))
		if err != nil {
			return err
		}
		if used+int(cs.Items)-staged > p.cap() {
			v.refuse(fmt.Sprintf("It would pass %d automatic changes in an hour.", p.cap()))
			return nil
		}
		usedCreates, err := createsSince(ctx, tx, cs.Plugin, e.now().Add(-time.Hour))
		if err != nil {
			return err
		}
		if usedCreates+staged > p.createCap() {
			v.refuse(fmt.Sprintf("It would pass %d new entries in an hour.", p.createCap()))
			return nil
		}
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
		if reason := checkStandingItem(ctx, tx, p, it, st); reason != "" {
			v.refuse(fmt.Sprintf("Change %d: %s", it.Seq, reason))
		}
	}
	return nil
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
func checkStandingItem(ctx context.Context, tx pgx.Tx, p StandingPolicy, it StoredItem, st *standingRun) string {
	if it.Kind != KindEntity {
		return "only page and record updates qualify."
	}
	if it.Op != OpUpsert {
		return "deleting never qualifies."
	}
	if len(it.BlobKeys) > 0 {
		return "files never qualify."
	}
	if isCleanCreate(ctx, tx, p, it) {
		return checkCreateItem(ctx, tx, p, it, st)
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
	if jsonx.Unmarshal(it.Payload, &pl, jsonx.StoredLoose) != nil {
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
		Properties map[string]jsontext.Value `json:"properties"`
	}
	if jsonx.Unmarshal(schemaDoc, &props, jsonx.StoredLoose) != nil {
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
	if jsonx.Unmarshal(raw, &m, jsonx.StoredLoose) != nil {
		return nil, false
	}
	return m, true
}

// safeScalar reports whether v is a value of a property the policy may touch:
// the schema declares one plain type, and v is that type (and, for a fixed
// choice, one of the choices).
func safeScalar(def jsontext.Value, v any) bool {
	var d struct {
		Type   string `json:"type"`
		Format string `json:"format"`
		Enum   []any  `json:"enum"`
	}
	if jsonx.Unmarshal(def, &d, jsonx.StoredLoose) != nil {
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

// standingRun is what the item checks of one changeset share: the creates
// already accepted, so later items may name them as parent or translation
// source (they apply in the same order).
type standingRun struct {
	created map[string]string // source key -> schema slug
	slugs   map[string]bool   // schema slug + "/" + lowercased explicit slug
}

// isCleanCreate reports whether the item is a plain create the policy may
// judge: an entity upsert with no target and no live or stale mapping. Anything
// else (a mapped key, a target that is gone) is not a create and keeps the
// update rule's refusal.
func isCleanCreate(ctx context.Context, tx rowQuerier, p StandingPolicy, it StoredItem) bool {
	if !p.createsEnabled() || it.Kind != KindEntity || it.Op != OpUpsert || it.TargetID != "" {
		return false
	}
	mapped, err := mappedTarget(ctx, tx, Origin(p.Plugin), it.Kind, it.SourceKey)
	return err == nil && mapped == ""
}

// Entity attribute keys the SEO block of a payload merges into, which a schema
// need not declare.
var undeclaredAttributeKeys = []string{"meta_title", "meta_description"}

// checkCreateItem returns why a new entry is outside the create rule, or "".
// Every check must pass; the writer applies the item as a human publish would.
func checkCreateItem(ctx context.Context, tx pgx.Tx, p StandingPolicy, it StoredItem, st *standingRun) string {
	var pl entityPayload
	if jsonx.Unmarshal(it.Payload, &pl, jsonx.StoredLoose) != nil {
		return "it could not be read."
	}
	if !slices.Contains(p.Create.Schemas, pl.Schema) {
		return "its type is not covered by the create rule."
	}
	if it.SourceKey != "" {
		if _, dup := st.created[it.SourceKey]; dup {
			return "two new entries share one source key."
		}
	}
	switch pl.Status {
	case "", "draft", "published":
	default:
		return "it sets a status other than draft or published."
	}
	if pl.ParentID != nil {
		return "a new entry may not be placed under an existing page."
	}
	if pl.SEO != nil && pl.SEO.Canonical != "" {
		return "it sets a canonical address."
	}
	origin := Origin(p.Plugin)
	if pl.ParentKey != "" {
		ok, reason := ownedOrStaged(ctx, tx, p, origin, pl.ParentKey, st, false)
		if !ok {
			return "its parent " + reason
		}
	}
	if pl.TranslationOf != "" {
		ok, reason := ownedOrStaged(ctx, tx, p, origin, pl.TranslationOf, st, true)
		if !ok {
			return "its translation source " + reason
		}
	}
	var schemaID uuid.UUID
	var schemaDoc []byte
	if err := tx.QueryRow(ctx, `SELECT id, schema FROM hatamoto_entity_schemas WHERE slug = $1`, pl.Schema).Scan(&schemaID, &schemaDoc); err != nil {
		return "its type could not be read."
	}
	if pl.Slug != "" {
		key := pl.Schema + "/" + strings.ToLower(pl.Slug)
		var taken bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM hatamoto_entities WHERE schema_id = $1 AND lower(slug) = lower($2) AND variant_key IS NULL)`,
			schemaID, pl.Slug).Scan(&taken); err != nil {
			return "its address could not be checked."
		}
		if taken || st.slugs[key] {
			return "its address is already taken."
		}
		st.slugs[key] = true
	}
	if reason := checkCreateAttributes(schemaDoc, pl.Attributes); reason != "" {
		return reason
	}
	if it.SourceKey != "" {
		st.created[it.SourceKey] = pl.Schema
	}
	return ""
}

// ownedOrStaged resolves a parent or translation reference exactly as the
// writer does and accepts only entities of this plugin in a listed schema, or
// an earlier create of this changeset. A translation_of that parses as an id is
// that entity (the writer never consults source keys then); a parent_key is
// only ever a source key.
func ownedOrStaged(ctx context.Context, tx pgx.Tx, p StandingPolicy, origin, ref string, st *standingRun, isTranslation bool) (bool, string) {
	id, err := uuid.Parse(ref)
	if !isTranslation || err != nil {
		if schema, ok := st.created[ref]; ok {
			if slices.Contains(p.Create.Schemas, schema) {
				return true, ""
			}
			return false, "is in a type the create rule does not cover."
		}
		target, err := liveEntityTarget(ctx, tx, origin, ref)
		if err != nil || target == "" {
			return false, "could not be found."
		}
		if id, err = uuid.Parse(target); err != nil {
			return false, "could not be found."
		}
	}
	var liveOrigin, schema string
	err = tx.QueryRow(ctx, `
		SELECT e.origin, s.slug FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id WHERE e.id = $1`, id).
		Scan(&liveOrigin, &schema)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "could not be found."
	}
	if err != nil {
		return false, "could not be read."
	}
	if liveOrigin != origin {
		return false, "is something the plugin did not create."
	}
	if !slices.Contains(p.Create.Schemas, schema) {
		return false, "is in a type the create rule does not cover."
	}
	return true, ""
}

// checkCreateAttributes refuses a new entry that carries a media reference:
// a non-empty value in a field the schema marks as an image or file, in a
// key the schema does not declare, or in a field it cannot classify.
func checkCreateAttributes(schemaDoc []byte, raw jsontext.Value) string {
	attrs, ok := decodeObject(raw)
	if !ok {
		return "its values could not be read."
	}
	var props struct {
		Properties map[string]any `json:"properties"`
	}
	if jsonx.Unmarshal(schemaDoc, &props, jsonx.StoredLoose) != nil {
		return "its type could not be read."
	}
	for k, v := range attrs {
		if isEmptyValue(v) {
			continue
		}
		def, declared := props.Properties[k]
		if !declared {
			if slices.Contains(undeclaredAttributeKeys, k) {
				continue
			}
			return fmt.Sprintf("it sends %q, which its type does not declare.", k)
		}
		if hasMediaMarker(def) || imageByName(k, def) {
			return fmt.Sprintf("%q holds an image or file, which never qualifies.", k)
		}
	}
	return ""
}

func isEmptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return !slices.ContainsFunc(x, func(e any) bool { return !isEmptyValue(e) })
	case map[string]any:
		for _, e := range x {
			if !isEmptyValue(e) {
				return false
			}
		}
		return true
	}
	return false
}

// imageByName applies the admin's naming rule to a schema node and to every
// property and array item below it. A node is named by its own key; array
// items take the array's key, as the admin does.
func imageByName(name string, node any) bool {
	n, ok := node.(map[string]any)
	if !ok {
		return false
	}
	format, _ := n["format"].(string)
	if schemaform.IsImageField(name, format, false) {
		return true
	}
	if props, ok := n["properties"].(map[string]any); ok {
		for k, child := range props {
			if imageByName(k, child) {
				return true
			}
		}
	}
	return imageByName(name, n["items"])
}

// hasMediaMarker reports whether a schema node, or any node below it, is an
// image or file field.
func hasMediaMarker(node any) bool {
	switch n := node.(type) {
	case map[string]any:
		if m, _ := n["x-media"].(bool); m {
			return true
		}
		if f, _ := n["x-hm-field"].(string); f == "image" || f == "file" {
			return true
		}
		for _, child := range n {
			if hasMediaMarker(child) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(n, hasMediaMarker)
	}
	return false
}
